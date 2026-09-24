package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// workspace is one Linear workspace (organization) you're signed in to. Each
// has its own tokens, in the keychain under account(ID).
type workspace struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URLKey string `json:"urlKey"`
}

// legacyAccount holds the tokens of the one workspace 0.2 and earlier signed
// in to; migrateLegacy moves them under their workspace's account.
const legacyAccount = "oauth"

func account(workspaceID string) string { return "oauth:" + workspaceID }

// workspaceIndex is what the picker remembers besides the tokens, in
// workspaces.json in the state directory: the workspaces you're signed in to,
// and which one each repo belongs to. Nothing secret.
type workspaceIndex struct {
	Workspaces []workspace `json:"workspaces"`
	// Repos maps a repo (its main checkout, so every worktree of it counts)
	// to a workspace ID.
	Repos map[string]string `json:"repos,omitempty"`
}

func indexPath() string { return filepath.Join(stateDir(), "workspaces.json") }

var errBrokenIndex = errors.New("unreadable")

func parseIndex() (workspaceIndex, error) {
	var ix workspaceIndex
	data, err := os.ReadFile(indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return ix, nil
	} else if err != nil {
		return ix, err
	}
	if err := json.Unmarshal(data, &ix); err != nil {
		return workspaceIndex{}, errBrokenIndex
	}
	return ix, nil
}

// readIndex reads the index. Writers replace it whole (updateIndexLocked),
// so it's never seen half-written; one that's unreadable anyway is set aside
// under the lock (readIndexLocked).
func readIndex() (workspaceIndex, error) {
	ix, err := parseIndex()
	if !errors.Is(err, errBrokenIndex) {
		return ix, err
	}
	unlock, err := lockTokens(context.Background())
	if err != nil {
		return workspaceIndex{}, err
	}
	defer unlock()
	return readIndexLocked()
}

// readIndexLocked reads the index, the caller holding the token lock. An
// unreadable one is moved to workspaces.json.broken rather than be stuck on:
// you'll be asked to choose or sign in again, which puts each workspace
// back. Under the lock, what's moved is what was read, not a good index
// another process wrote since.
func readIndexLocked() (workspaceIndex, error) {
	ix, err := parseIndex()
	if !errors.Is(err, errBrokenIndex) {
		return ix, err
	}
	if err := os.Rename(indexPath(), indexPath()+".broken"); err != nil {
		return workspaceIndex{}, fmt.Errorf("%s is unreadable and couldn't be set aside: %w", indexPath(), err)
	}
	return workspaceIndex{}, nil
}

// updateIndex changes the index under the token lock, so two popups can't
// lose each other's changes, and writes it whole or not at all.
func updateIndex(ctx context.Context, change func(*workspaceIndex)) error {
	unlock, err := lockTokens(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return updateIndexLocked(change)
}

func updateIndexLocked(change func(*workspaceIndex)) error {
	ix, err := readIndexLocked()
	if err != nil {
		return err
	}
	change(&ix)
	data, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir(), "workspaces-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), indexPath())
}

func (ix *workspaceIndex) add(w workspace) {
	for i := range ix.Workspaces {
		if ix.Workspaces[i].ID == w.ID {
			ix.Workspaces[i] = w // a renamed workspace keeps its ID
			return
		}
	}
	ix.Workspaces = append(ix.Workspaces, w)
}

func (ix *workspaceIndex) remove(id string) {
	ix.Workspaces = slices.DeleteFunc(ix.Workspaces, func(w workspace) bool { return w.ID == id })
	for repo, ws := range ix.Repos {
		if ws == id {
			delete(ix.Repos, repo)
		}
	}
}

func (ix *workspaceIndex) remember(repo, id string) {
	if repo == "" {
		return
	}
	if ix.Repos == nil {
		ix.Repos = map[string]string{}
	}
	ix.Repos[repo] = id
}

func (ix workspaceIndex) byID(id string) *workspace {
	for i := range ix.Workspaces {
		if ix.Workspaces[i].ID == id {
			return &ix.Workspaces[i]
		}
	}
	return nil
}

// find matches a workspace as typed on the command line: its URL key, which
// is unique, else its name, if only one has it.
func (ix workspaceIndex) find(s string) (*workspace, error) {
	var named []*workspace
	for i := range ix.Workspaces {
		w := &ix.Workspaces[i]
		if strings.EqualFold(w.URLKey, s) {
			return w, nil
		}
		if strings.EqualFold(w.Name, s) {
			named = append(named, w)
		}
	}
	switch len(named) {
	case 0:
		return nil, fmt.Errorf("not signed in to a workspace called %q", s)
	case 1:
		return named[0], nil
	}
	keys := make([]string, len(named))
	for i, w := range named {
		keys[i] = w.URLKey
	}
	return nil, fmt.Errorf("more than one workspace is called %q; name it by its URL key: %s", s, strings.Join(keys, ", "))
}

// pick is the workspace for a repo without asking: the only one, or the one
// the repo was given. nil means ask.
func (ix workspaceIndex) pick(repo string) *workspace {
	if len(ix.Workspaces) == 1 {
		return &ix.Workspaces[0]
	}
	if id, ok := ix.Repos[repo]; ok && repo != "" {
		return ix.byID(id)
	}
	return nil
}

// repoKey names the repo a directory is in by its main checkout, so the
// repo's worktrees share its workspace. Outside git, the directory itself;
// symlinks resolved either way, so one repo has one name.
func repoKey(dir string) string {
	if dir == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	rev := func(arg string) (string, bool) {
		out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", arg).Output()
		return strings.TrimSpace(string(out)), err == nil
	}
	common, ok := rev("--git-common-dir")
	if !ok {
		return filepath.Clean(dir)
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	// A submodule keeps its repo in the superproject's .git/modules: name it
	// by its own checkout. Only a bare repo has none.
	if top, ok := rev("--show-toplevel"); ok {
		return top
	}
	return common
}

// whoami asks Linear which workspace a token belongs to. A var for tests.
var whoami = func(ctx context.Context, cfg config, accessToken string) (workspace, error) {
	var reply struct {
		Organization workspace `json:"organization"`
	}
	c := &linearClient{cfg: cfg}
	status, errs, data, err := c.post(ctx, accessToken, `query { organization { id name urlKey } }`, nil)
	switch {
	case err != nil:
		return workspace{}, err
	case isAuthError(status, errs):
		return workspace{}, errSignedOut
	case len(errs) > 0:
		return workspace{}, fmt.Errorf("Linear: %s", clean(errs[0].Message, false))
	case status != 200:
		return workspace{}, fmt.Errorf("Linear: HTTP %d", status)
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return workspace{}, err
	}
	sanitize(&reply)
	if reply.Organization.ID == "" {
		return workspace{}, errors.New("Linear didn't say which workspace this is")
	}
	return reply.Organization, nil
}

// migrateLegacy moves a 0.2 sign-in to its workspace's account. Without one
// it does nothing.
func migrateLegacy(ctx context.Context, cfg config) error {
	for range 3 {
		if _, err := readStore(legacyAccount); errors.Is(err, errSignedOut) {
			return nil
		} else if err != nil {
			return err
		}
		token, err := accessToken(ctx, cfg, legacyAccount, false)
		if err != nil {
			return err
		}
		w, err := whoami(ctx, cfg, token)
		if err != nil {
			return err
		}
		if done, err := moveLegacy(ctx, cfg, token, w); done || err != nil {
			return err
		}
	}
	return errors.New("your Linear sign-in kept changing while it was being moved; open the picker again")
}

// moveLegacy moves the 0.2 tokens to w's account, if they're still the ones
// whose access token was identified as w: if they changed meanwhile (another
// process refreshed them, or an old version signed in again), it says not
// done, so they're identified again.
//
// When w's account already has other tokens (a sign-in since the upgrade,
// or a move that stopped short of deleting the old item), which pair is
// current can't be told from the pairs themselves, so Linear is asked: the
// account's pair stays if it still works, and the 0.2 pair replaces it if
// Linear has revoked it. Either way the other is forgotten, not revoked:
// they may be the same grant. If Linear can't answer, nothing changes.
func moveLegacy(ctx context.Context, cfg config, identified string, w workspace) (bool, error) {
	unlock, err := lockTokens(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	t, err := readStore(legacyAccount)
	if errors.Is(err, errSignedOut) {
		return true, nil // another process moved it
	} else if err != nil {
		return false, err
	}
	if t.AccessToken != identified {
		return false, nil
	}
	cur, err := readStore(account(w.ID))
	if err != nil && !errors.Is(err, errSignedOut) {
		return false, err
	}
	replace := cur == nil
	if cur != nil && cur.RefreshToken != t.RefreshToken {
		live, err := worksFor(ctx, cfg, account(w.ID), cur, w)
		if err != nil {
			return false, err
		}
		replace = !live
	}
	if err := updateIndexLocked(func(ix *workspaceIndex) { ix.add(w) }); err != nil {
		return false, err
	}
	if replace {
		if err := writeStore(account(w.ID), t); err != nil {
			return false, err
		}
	}
	return true, removeStore(legacyAccount)
}

// worksFor asks Linear whether the tokens t, stored under acct, still work
// for w: refreshed first if due (which stores the new pair), then asked
// which workspace they're for. false means Linear refused them; an error
// means it couldn't say. The caller holds the token lock.
func worksFor(ctx context.Context, cfg config, acct string, t *tokens, w workspace) (bool, error) {
	if !fresh(t) {
		nt, err := refreshLocked(ctx, cfg, acct, t)
		if errors.Is(err, errSignedOut) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		t = nt
	}
	got, err := whoami(ctx, cfg, t.AccessToken)
	if errors.Is(err, errSignedOut) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return got.ID == w.ID, nil
}

// loadWorkspaces is the index after moving any 0.2 sign-in into it. If that
// fails, the index is still returned, with the error: the workspaces already
// in it work regardless.
func loadWorkspaces(ctx context.Context, cfg config) (workspaceIndex, error) {
	migrateErr := migrateLegacy(ctx, cfg)
	ix, err := readIndex()
	if err != nil {
		return ix, err
	}
	return ix, migrateErr
}

// forWorkspace is cfg with "repos" settled for one workspace: a key
// "urlKey/TEAM" is that workspace's alone and wins over a plain "TEAM"; other
// workspaces' keys drop out.
func (cfg config) forWorkspace(urlKey string) config {
	repos := map[string]string{}
	for k, v := range cfg.Repos {
		if !strings.Contains(k, "/") {
			repos[k] = v
		}
	}
	for k, v := range cfg.Repos {
		if ws, team, ok := strings.Cut(k, "/"); ok && strings.EqualFold(ws, urlKey) {
			repos[team] = v
		}
	}
	cfg.Repos = repos
	return cfg
}
