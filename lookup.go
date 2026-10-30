package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// `issues` is for programs rather than people: given issue keys, it prints
// what Linear says about each, as JSON. The Herdr app's recap shows agents
// from several machines and, not running herdr's plugin hooks, has no
// $issue tokens from tick; it asks here instead, on the machine that's
// signed in, for agents anywhere.
//
// It finds an issue as tick does: by the team keys each signed-in workspace
// has, then one query per workspace for all of its keys. Answers are kept in
// lookups.json for a minute, so a caller asking every few seconds reaches
// Linear about once a minute; --force asks again regardless.

const (
	lookupFresh     = 60 * time.Second // how long an answer is served without asking Linear
	lookupKeep      = 24 * time.Hour   // older answers are dropped from the file
	lookupTimeLimit = 30 * time.Second
)

// lookupState is lookups.json: the team keys of each workspace, and the
// last answer for each key.
type lookupState struct {
	Teams  map[string]teamKeys    `json:"teams"`  // Linear workspace ID → its teams' keys
	Issues map[string]lookupEntry `json:"issues"` // "HAL-2066" → what Linear said
}

type lookupEntry struct {
	Fetched   time.Time    `json:"fetched"`
	Workspace string       `json:"workspace,omitempty"` // its URL key
	Issue     *taggedIssue `json:"issue"`               // nil: no workspace has it
}

// lookupResult is one element of what `issues` prints, in the order the keys
// were given. Error is set only when the key couldn't be answered; Issue is
// then the last answer known, if any.
type lookupResult struct {
	Key   string         `json:"key"`
	Issue *lookedUpIssue `json:"issue"`
	Error string         `json:"error,omitempty"`
}

type lookedUpIssue struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	State      string `json:"state"`
	StateType  string `json:"state_type"` // triage | backlog | unstarted | started | completed | canceled
	Workspace  string `json:"workspace"`  // the Linear workspace's URL key
}

func lookupStatePath() string { return filepath.Join(stateDir(), "lookups.json") }

func readLookupState() lookupState {
	var s lookupState
	if data, err := os.ReadFile(lookupStatePath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.Teams == nil {
		s.Teams = map[string]teamKeys{}
	}
	if s.Issues == nil {
		s.Issues = map[string]lookupEntry{}
	}
	return s
}

func writeLookupState(s lookupState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := lookupStatePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, lookupStatePath())
}

// issuesArgs reads `issues [--force] JSON`, in either order.
func issuesArgs(args []string) (keys []string, force bool, err error) {
	var raw string
	for _, a := range args {
		switch {
		case a == "--force":
			force = true
		case strings.HasPrefix(a, "-"):
			return nil, false, fmt.Errorf("issues: unknown option %q", a)
		case raw != "":
			return nil, false, errors.New("issues: give the keys as one JSON array")
		default:
			raw = a
		}
	}
	if raw == "" {
		return nil, false, errors.New(`issues needs a JSON array of keys, like '["ENG-142"]'`)
	}
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, false, fmt.Errorf("issues: not a JSON array of strings: %w", err)
	}
	return keys, force, nil
}

// parseIssueKey reads a key such as "HAL-2066" (any case, spaces around it
// ignored). Anything else, a branch name included, isn't one.
func parseIssueKey(s string) (issueRef, bool) {
	s = strings.TrimSpace(s)
	refs := branchRefs(s)
	if len(refs) != 1 || !strings.EqualFold(refs[0].String(), s) {
		return issueRef{}, false
	}
	return refs[0], true
}

func (e lookupEntry) result() *lookedUpIssue {
	if e.Issue == nil {
		return nil
	}
	return &lookedUpIssue{
		Identifier: e.Issue.Identifier, Title: e.Issue.Title, URL: e.Issue.URL,
		State: e.Issue.State, StateType: e.Issue.StateType, Workspace: e.Workspace,
	}
}

// runIssues prints lookupIssues' answer for the keys as one JSON array.
func runIssues(ctx context.Context, cfg config, args []string, out io.Writer) error {
	keys, force, err := issuesArgs(args)
	if err != nil {
		return err
	}
	results, err := lookupIssues(ctx, cfg, keys, force)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(results)
}

// lookupIssues answers each key, in order. Only a broken state directory
// fails the whole call; a key that can't be answered says why in its own
// element.
func lookupIssues(ctx context.Context, cfg config, keys []string, force bool) ([]lookupResult, error) {
	results := make([]lookupResult, len(keys))
	if len(keys) == 0 {
		return results, nil
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return nil, err
	}
	// One at a time: a second caller waits, then finds the first's answers.
	lock, err := os.OpenFile(filepath.Join(stateDir(), "lookups.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	st := readLookupState()
	now := nowFn()
	refs := make([]issueRef, len(keys))
	var due []issueRef
	isDue := map[issueRef]bool{}
	for i, k := range keys {
		results[i].Key = k
		ref, ok := parseIssueKey(k)
		if !ok {
			results[i].Error = "not an issue key"
			continue
		}
		refs[i] = ref
		if e, ok := st.Issues[ref.String()]; ok && !force && now.Sub(e.Fetched) < lookupFresh {
			results[i].Issue = e.result()
			continue
		}
		if !isDue[ref] {
			isDue[ref] = true
			due = append(due, ref)
		}
	}
	if len(due) == 0 {
		return results, nil
	}

	ctx, cancel := context.WithTimeout(ctx, lookupTimeLimit)
	defer cancel()
	failed := askLinear(ctx, cfg, &st, due, now, force)

	for i := range results {
		ref := refs[i]
		if !isDue[ref] {
			continue
		}
		if err, ok := failed[ref]; ok {
			results[i].Error = err.Error()
		}
		if e, ok := st.Issues[ref.String()]; ok {
			results[i].Issue = e.result() // fresh, or the last known when it failed
		}
	}
	for k, e := range st.Issues {
		if now.Sub(e.Fetched) >= lookupKeep {
			delete(st.Issues, k)
		}
	}
	if err := writeLookupState(st); err != nil {
		return nil, err
	}
	return results, nil
}

// askLinear looks up the due keys, recording each answer in st, and returns
// the ones it couldn't answer, with why.
func askLinear(ctx context.Context, cfg config, st *lookupState, due []issueRef, now time.Time, force bool) map[issueRef]error {
	failed := map[issueRef]error{}
	ix, err := signedInFn(ctx, cfg)
	if len(ix.Workspaces) == 0 {
		if err == nil {
			err = errNotSignedIn
		}
		for _, ref := range due {
			failed[ref] = err
		}
		return failed
	}

	// Each workspace's team keys: this file's, else tick's in labels.json,
	// else Linear's.
	labelTeams := readLabelState().Teams
	wsErr := map[string]error{}
	signedIn := map[string]bool{}
	for _, lw := range ix.Workspaces {
		signedIn[lw.ID] = true
		if t, ok := st.Teams[lw.ID]; ok && !force && now.Sub(t.Fetched) < teamsFresh {
			continue
		}
		if t, ok := labelTeams[lw.ID]; ok && !force && now.Sub(t.Fetched) < teamsFresh {
			st.Teams[lw.ID] = t
			continue
		}
		keys, err := teamKeysFn(ctx, cfg.forWorkspace(lw.URLKey), lw)
		if err != nil {
			wsErr[lw.ID] = err
			continue
		}
		st.Teams[lw.ID] = teamKeys{Fetched: now, Keys: keys}
	}
	for id := range st.Teams {
		if !signedIn[id] {
			delete(st.Teams, id) // signed out of since
		}
	}

	// Which workspaces could have each key, and one query per workspace.
	owners := map[issueRef][]workspace{}
	byWs := map[string][]issueRef{}
	for _, ref := range due {
		for _, lw := range ix.Workspaces {
			if t, ok := st.Teams[lw.ID]; ok && wsErr[lw.ID] == nil && t.has(ref.Team) {
				owners[ref] = append(owners[ref], lw)
				byWs[lw.ID] = append(byWs[lw.ID], ref)
			}
		}
	}
	found := map[string]map[issueRef]taggedIssue{}
	for _, lw := range ix.Workspaces {
		refs := byWs[lw.ID]
		for i := 0; i < len(refs) && wsErr[lw.ID] == nil; i += issuesPerQ {
			got, err := issueLookupFn(ctx, cfg.forWorkspace(lw.URLKey), lw, refs[i:min(len(refs), i+issuesPerQ)])
			if err != nil {
				wsErr[lw.ID] = err
				break
			}
			if found[lw.ID] == nil {
				found[lw.ID] = map[issueRef]taggedIssue{}
			}
			for r, is := range got {
				found[lw.ID][r] = is
			}
		}
	}

	// The first workspace (in sign-in order) that has it; nobody's when
	// every workspace that could have it answered without it. A workspace
	// that couldn't say leaves the key unanswered.
	for _, ref := range due {
		var answer *lookupEntry
		var why error
		for _, lw := range owners[ref] {
			if err := wsErr[lw.ID]; err != nil {
				why = fmt.Errorf("%s: %w", lw.URLKey, err)
				continue
			}
			if is, ok := found[lw.ID][ref]; ok {
				is := is
				answer = &lookupEntry{Fetched: now, Workspace: lw.URLKey, Issue: &is}
				break
			}
		}
		if len(owners[ref]) == 0 {
			// No team by that key, as far as is known: nobody's, unless a
			// workspace couldn't be asked for its teams.
			for _, lw := range ix.Workspaces {
				if err := wsErr[lw.ID]; err != nil {
					why = fmt.Errorf("%s: %w", lw.URLKey, err)
					break
				}
			}
		}
		switch {
		case answer != nil:
			st.Issues[ref.String()] = *answer
		case why != nil:
			failed[ref] = why
		default:
			st.Issues[ref.String()] = lookupEntry{Fetched: now}
		}
	}
	return failed
}
