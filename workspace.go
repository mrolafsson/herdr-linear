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

func readIndex() (workspaceIndex, error) {
	var ix workspaceIndex
	data, err := os.ReadFile(indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return ix, nil
	} else if err != nil {
		return ix, err
	}
	if err := json.Unmarshal(data, &ix); err != nil {
		return workspaceIndex{}, fmt.Errorf("%s: %w", indexPath(), err)
	}
	return ix, nil
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
	ix, err := readIndex()
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

// find matches a workspace by its URL key or name, as typed on the command line.
func (ix workspaceIndex) find(s string) *workspace {
	for i := range ix.Workspaces {
		if w := &ix.Workspaces[i]; strings.EqualFold(w.URLKey, s) || strings.EqualFold(w.Name, s) {
			return w
		}
	}
	return nil
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
// repo's worktrees share its workspace. Outside git, the directory itself.
func repoKey(dir string) string {
	if dir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return filepath.Clean(dir)
	}
	common := strings.TrimSpace(string(out))
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common // a bare repo
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
	unlock, err := lockTokens(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	t, err := readStore(legacyAccount) // re-read: a refresh may have rotated it
	if errors.Is(err, errSignedOut) {
		return nil // another process migrated it meanwhile
	} else if err != nil {
		return err
	}
	if err := writeStore(account(w.ID), t); err != nil {
		return err
	}
	if err := updateIndexLocked(func(ix *workspaceIndex) { ix.add(w) }); err != nil {
		return err
	}
	return removeStore(legacyAccount)
}

// loadWorkspaces is the index after any 0.2 sign-in has been moved into it.
func loadWorkspaces(ctx context.Context, cfg config) (workspaceIndex, error) {
	if err := migrateLegacy(ctx, cfg); err != nil {
		return workspaceIndex{}, err
	}
	return readIndex()
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
