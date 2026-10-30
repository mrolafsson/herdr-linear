package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The sidebar labels. On herdr's events (and at startup) `tick` looks at the
// branch every space and agent is on, finds the Linear issue it's named for,
// and reports it as display-only tokens: $issue ("ENG-142"), $issue_state
// ("In Progress") and $issue_title. You show them by adding e.g. "$issue" to
// a sidebar row; herdr-recap lists them with each agent.
//
// A branch is an issue's when it has the issue's identifier in it, as the
// branch names Linear suggests do ("me/eng-142-fix-login"), and only for a
// team the workspace has: "utf-8" and "step-2" are nobody's.
//
// It is cheap when nothing is due: a tick within a few seconds of the last
// does nothing, and Linear is only asked about an issue whose answer is older
// than label_refresh_seconds.

const (
	tokenSource = "herdr-linear"
	// Tokens outlive a quiet spell but not a stale plugin: each tick that
	// finds them older than half this re-reports them.
	tokenTTL      = 2 * time.Hour
	tickCooldown  = 3 * time.Second
	tickTimeLimit = 30 * time.Second
	teamsFresh    = 24 * time.Hour // how long a workspace's team keys are trusted
	issuesPerQ    = 40             // issues looked up per GraphQL query
	titleLength   = 60
)

// tokenNames are every token this plugin sets, so a space that left its
// issue's branch has all of them cleared.
var tokenNames = []string{"issue", "issue_state", "issue_title"}

// labelState is labels.json: what Linear last said per issue, and when the
// tokens were last sent.
type labelState struct {
	LastTick time.Time             `json:"last_tick"`
	Teams    map[string]teamKeys   `json:"teams"`    // Linear workspace ID → its teams' keys
	Issues   map[string]issueEntry `json:"issues"`   // Linear workspace ID + "\x00" + identifier
	Reported map[string]reported   `json:"reported"` // space or pane ID → what was sent
	Failed   map[string]time.Time  `json:"failed"`   // Linear workspace ID → when it last failed to answer
}

type teamKeys struct {
	Fetched time.Time `json:"fetched"`
	Keys    []string  `json:"keys"` // upper case
}

func (t teamKeys) has(key string) bool {
	for _, k := range t.Keys {
		if k == key {
			return true
		}
	}
	return false
}

type issueEntry struct {
	Fetched time.Time    `json:"fetched"`
	Issue   *taggedIssue `json:"issue"` // nil: Linear has no such issue
}

// taggedIssue is as much of an issue as the tokens say, and its address and
// kind of state for `issues` (lookup.go).
type taggedIssue struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	State      string `json:"state"`
	StateType  string `json:"state_type,omitempty"`
	URL        string `json:"url,omitempty"`
}

type reported struct {
	Tokens map[string]string `json:"tokens"`
	At     time.Time         `json:"at"`
	Issue  string            `json:"issue"` // the issue (cache key) they were for
}

func labelStatePath() string { return filepath.Join(stateDir(), "labels.json") }

func readLabelState() labelState {
	var s labelState
	if data, err := os.ReadFile(labelStatePath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.Teams == nil {
		s.Teams = map[string]teamKeys{}
	}
	if s.Issues == nil {
		s.Issues = map[string]issueEntry{}
	}
	if s.Reported == nil {
		s.Reported = map[string]reported{}
	}
	if s.Failed == nil {
		s.Failed = map[string]time.Time{}
	}
	return s
}

func writeLabelState(s labelState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := labelStatePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, labelStatePath())
}

// issueRef names an issue as a branch does: a team's key and a number.
type issueRef struct {
	Team   string // upper case
	Number int
}

func (r issueRef) String() string { return r.Team + "-" + strconv.Itoa(r.Number) }

// A team key and a number, as Linear writes identifiers; what's around it is
// checked by hand (RE2 has no lookaround).
var refPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9]{0,9}-[0-9]{1,7}`)

func alnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// branchRefs are the issues a branch name could be for, in the order they're
// written: every "key-123" standing on its own. Whether there's a team by
// that key is for the caller to say.
func branchRefs(branch string) []issueRef {
	var refs []issueRef
	for at := 0; at < len(branch); {
		loc := refPattern.FindStringIndex(branch[at:])
		if loc == nil {
			break
		}
		start, end := at+loc[0], at+loc[1]
		if (start > 0 && alnum(branch[start-1])) || (end < len(branch) && alnum(branch[end])) {
			// Part of a longer word: look again from its next piece.
			if i := strings.IndexAny(branch[start:], "/_-"); i >= 0 {
				at = start + i + 1
				continue
			}
			break
		}
		key, num, _ := strings.Cut(branch[start:end], "-")
		if n, err := strconv.Atoi(num); err == nil && n > 0 {
			refs = append(refs, issueRef{strings.ToUpper(key), n})
		}
		// "a-1-b-2": the next may start right after this one's number.
		at = end
	}
	return refs
}

// issueTokens are the tokens for an issue; nil means "no issue": clear them.
func issueTokens(is *taggedIssue) map[string]string {
	if is == nil {
		return nil
	}
	t := map[string]string{"issue": is.Identifier}
	if is.State != "" {
		t["issue_state"] = is.State
	}
	if title := shorten(strings.Join(strings.Fields(is.Title), " "), titleLength); title != "" {
		t["issue_title"] = title
	}
	return t
}

// tokenParams turns tokens into report_metadata's map: every name we own,
// with null for the ones not set now, so stale ones are cleared.
func tokenParams(t map[string]string) map[string]any {
	out := map[string]any{}
	for _, n := range tokenNames {
		if v, ok := t[n]; ok {
			out[n] = v
		} else {
			out[n] = nil
		}
	}
	return out
}

func sameTokens(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// shown picks our tokens out of all that a space or pane shows.
func shown(current map[string]string) map[string]string {
	ours := map[string]string{}
	for _, n := range tokenNames {
		if v, ok := current[n]; ok {
			ours[n] = v
		}
	}
	return ours
}

// Seams for tests.
var (
	listWorkspacesFn = listWorkspaces
	listPanesFn      = func() ([]paneInfo, error) { return listPanes("") }
	reportFn         = func(method string, params map[string]any) error { return herdrCall(method, params, nil) }
	signedInFn       = func(ctx context.Context, cfg config) (workspaceIndex, error) { return loadWorkspaces(ctx, cfg) }
	branchFn         = func(ctx context.Context, dir string) string {
		b, _ := git(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
		return b
	}
	repoKeyFn  = repoKey
	teamKeysFn = func(ctx context.Context, cfg config, w workspace) ([]string, error) {
		ts, err := (&linearClient{cfg: cfg, ws: w}).teams(ctx)
		keys := make([]string, 0, len(ts))
		for _, t := range ts {
			keys = append(keys, strings.ToUpper(t.Key))
		}
		return keys, err
	}
	issueLookupFn = func(ctx context.Context, cfg config, w workspace, refs []issueRef) (map[issueRef]taggedIssue, error) {
		return (&linearClient{cfg: cfg, ws: w}).issuesByRefs(ctx, refs)
	}
	nowFn = time.Now
)

// issuesByRefs finds the issues by those identifiers, open or closed,
// anyone's, in one query. One that isn't there is left out.
func (c *linearClient) issuesByRefs(ctx context.Context, refs []issueRef) (map[issueRef]taggedIssue, error) {
	var res struct {
		Issues struct {
			Nodes []struct {
				Identifier string `json:"identifier"`
				Number     int    `json:"number"`
				Title      string `json:"title"`
				URL        string `json:"url"`
				State      struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"state"`
				Team team `json:"team"`
			} `json:"nodes"`
		} `json:"issues"`
	}
	either := make([]map[string]any, len(refs))
	for i, r := range refs {
		either[i] = map[string]any{
			"number": map[string]any{"eq": r.Number},
			"team":   map[string]any{"key": map[string]any{"eqIgnoreCase": r.Team}},
		}
	}
	q := `query($filter: IssueFilter) { issues(first: 100, filter: $filter) { nodes { identifier number title url state { name type } team { id key } } } }`
	if err := c.query(ctx, q, map[string]any{"filter": map[string]any{"or": either}}, &res); err != nil {
		return nil, err
	}
	out := map[issueRef]taggedIssue{}
	for _, n := range res.Issues.Nodes {
		out[issueRef{strings.ToUpper(n.Team.Key), n.Number}] = taggedIssue{Identifier: n.Identifier, Title: n.Title, State: n.State.Name, StateType: n.State.Type, URL: n.URL}
	}
	return out, nil
}

// tagged is the issue a directory's branch is for: whose (the Linear
// workspace) and which.
type tagged struct {
	ws  string
	ref issueRef
}

func (t tagged) cacheKey() string { return t.ws + "\x00" + t.ref.String() }

// tick refreshes the sidebar labels. force skips the cooldown and asks
// Linear again whatever the cache's age.
func tick(ctx context.Context, cfg config, force bool) error {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	// One tick at a time; one arriving while another runs has nothing to add.
	lock, err := os.OpenFile(filepath.Join(stateDir(), "tick.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if !force {
			return nil
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			return err
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	st := readLabelState()
	now := nowFn()
	if !force && now.Sub(st.LastTick) < tickCooldown {
		return nil
	}
	st.LastTick = now
	ctx, cancel := context.WithTimeout(ctx, tickTimeLimit)
	defer cancel()

	wss, err := listWorkspacesFn()
	if err != nil {
		return err
	}
	panes, err := listPanesFn()
	if err != nil {
		return err
	}
	ix, _ := signedInFn(ctx, cfg)
	if !cfg.labelsOn() || len(ix.Workspaces) == 0 {
		// Turned off, or signed out: nothing here is known to be so.
		return clearAll(st, wss, panes)
	}

	// Each Linear workspace's teams: what makes "eng-142" an issue.
	var lookupErr error
	rested := func(id string) bool {
		failed, ok := st.Failed[id]
		return ok && !force && now.Sub(failed) < time.Duration(cfg.LabelRefreshSeconds)*time.Second
	}
	byID := map[string]workspace{}
	for _, lw := range ix.Workspaces {
		byID[lw.ID] = lw
		if t, ok := st.Teams[lw.ID]; (ok && now.Sub(t.Fetched) < teamsFresh) || rested(lw.ID) {
			continue
		}
		keys, err := teamKeysFn(ctx, cfg.forWorkspace(lw.URLKey), lw)
		if err != nil {
			// Signed out or offline: go on what was known.
			lookupErr = err
			st.Failed[lw.ID] = now
			continue
		}
		delete(st.Failed, lw.ID)
		st.Teams[lw.ID] = teamKeys{Fetched: now, Keys: keys}
	}
	for id := range st.Teams {
		if _, ok := byID[id]; !ok {
			delete(st.Teams, id) // signed out of since
		}
	}

	// What each directory's branch is for. The repo's own Linear workspace
	// if it has one (the only one, or the one it was given in the popup),
	// or else the one workspace with a team by that key.
	dirs := map[string]*tagged{}
	tagOf := func(dir string) *tagged {
		if dir == "" {
			return nil
		}
		if t, ok := dirs[dir]; ok {
			return t
		}
		dirs[dir] = nil
		refs := branchRefs(branchFn(ctx, dir))
		if len(refs) == 0 {
			return nil
		}
		own := ix.pick(repoKeyFn(dir))
		for _, ref := range refs {
			var owners []string
			for id, t := range st.Teams {
				if t.has(ref.Team) && (own == nil || own.ID == id) {
					owners = append(owners, id)
				}
			}
			if len(owners) == 1 {
				dirs[dir] = &tagged{owners[0], ref}
				break
			}
		}
		return dirs[dir]
	}
	spaces := map[string]*tagged{}
	agents := map[string]*tagged{}
	for _, w := range wss {
		if w.Worktree != nil {
			spaces[w.WorkspaceID] = tagOf(w.Worktree.CheckoutPath)
		}
	}
	for _, p := range panes {
		if p.Agent == "" {
			continue // only agents are labelled; a shell keeps its own
		}
		// An agent is where it was started; one with no folder of its own
		// goes by its space's.
		if agents[p.PaneID] = tagOf(p.Cwd); agents[p.PaneID] == nil && p.Cwd == "" {
			agents[p.PaneID] = spaces[p.WorkspaceID]
		}
	}

	// Ask Linear about the issues whose answer is due, a workspace at a time.
	fresh := time.Duration(cfg.LabelRefreshSeconds) * time.Second
	due := map[string][]issueRef{}
	inUse := map[string]bool{}
	for _, t := range dirs {
		if t == nil || inUse[t.cacheKey()] {
			continue
		}
		inUse[t.cacheKey()] = true
		if e, ok := st.Issues[t.cacheKey()]; !rested(t.ws) && (force || !ok || now.Sub(e.Fetched) >= fresh) {
			due[t.ws] = append(due[t.ws], t.ref)
		}
	}
	for id, refs := range due {
		lw := byID[id]
		for i := 0; i < len(refs); i += issuesPerQ {
			batch := refs[i:min(len(refs), i+issuesPerQ)]
			found, err := issueLookupFn(ctx, cfg.forWorkspace(lw.URLKey), lw, batch)
			if err != nil {
				lookupErr = err
				st.Failed[id] = now
				break
			}
			delete(st.Failed, id)
			for _, ref := range batch {
				e := issueEntry{Fetched: now}
				if is, ok := found[ref]; ok {
					is := is
					e.Issue = &is
				}
				st.Issues[tagged{id, ref}.cacheKey()] = e
			}
		}
	}

	// Report: to each space, and each agent pane.
	live := map[string]bool{}
	tell := func(kind, id string, t *tagged, current map[string]string) {
		live[id] = true
		key := ""
		var is *taggedIssue
		if t != nil {
			key = t.cacheKey()
			e, known := st.Issues[key]
			switch {
			case known:
				is = e.Issue
			case st.Reported[id].Issue == key || st.Reported[id].Issue == "":
				return // Linear couldn't say: leave what's shown
			}
		}
		st.report(kind, id, issueTokens(is), current, now, key)
	}
	for _, w := range wss {
		tell("workspace", w.WorkspaceID, spaces[w.WorkspaceID], w.Tokens)
	}
	for _, p := range panes {
		tell("pane", p.PaneID, agents[p.PaneID], p.Tokens)
	}
	// Forget spaces and panes that are gone, and issues nobody is on.
	for id := range st.Reported {
		if !live[id] {
			delete(st.Reported, id)
		}
	}
	for k := range st.Issues {
		if !inUse[k] {
			delete(st.Issues, k)
		}
	}
	if err := writeLabelState(st); err != nil {
		return err
	}
	return lookupErr
}

// report sends tokens to a space or pane when they differ from what it shows
// (current, as herdr lists it) or are getting old.
func (st *labelState) report(kind, id string, want, current map[string]string, now time.Time, issue string) {
	ours := shown(current)
	last, sent := st.Reported[id]
	switch {
	case want == nil && len(ours) == 0:
		delete(st.Reported, id)
		return
	case sameTokens(want, ours) && sent && now.Sub(last.At) < tokenTTL/2:
		last.Issue = issue
		st.Reported[id] = last
		return
	}
	params := map[string]any{"source": tokenSource, "tokens": tokenParams(want)}
	if want != nil {
		params["ttl_ms"] = tokenTTL.Milliseconds()
	}
	method := "workspace.report_metadata"
	if kind == "pane" {
		method, params["pane_id"] = "pane.report_metadata", id
	} else {
		params["workspace_id"] = id
	}
	if err := reportFn(method, params); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-linear: labels:", err)
		return
	}
	if want == nil {
		delete(st.Reported, id)
	} else {
		st.Reported[id] = reported{Tokens: want, At: now, Issue: issue}
	}
}

// clearAll removes every label, for when they're turned off or you're
// signed out.
func clearAll(st labelState, wss []workspaceInfo, panes []paneInfo) error {
	now := nowFn()
	for _, w := range wss {
		st.report("workspace", w.WorkspaceID, nil, w.Tokens, now, "")
	}
	for _, p := range panes {
		st.report("pane", p.PaneID, nil, p.Tokens, now, "")
	}
	st.Issues = map[string]issueEntry{}
	return writeLabelState(st)
}

// isQuietTickErr says whether a tick failure is expected (signed out,
// offline, herdr not there) and not worth a line in herdr's plugin log on
// every event.
func isQuietTickErr(err error) bool {
	return errors.Is(err, errSignedOut) || errors.Is(err, errNotSignedIn) || errors.Is(err, context.DeadlineExceeded)
}
