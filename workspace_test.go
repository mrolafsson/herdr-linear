package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	acme   = workspace{ID: "org-acme", Name: "Acme", URLKey: "acme"}
	globex = workspace{ID: "org-globex", Name: "Globex", URLKey: "globex"}
)

// fakeStores swaps the keychain for memory, one item per account, and
// answers whoami from the access token: "acme-…" is Acme, anything else Globex.
func fakeStores(t *testing.T, initial map[string]*tokens) map[string]*tokens {
	t.Helper()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	var mu sync.Mutex
	items := map[string]*tokens{}
	for k, v := range initial {
		items[k] = v
	}
	oldR, oldW, oldD, oldWho := readStore, writeStore, removeStore, whoami
	readStore = func(acct string) (*tokens, error) {
		mu.Lock()
		defer mu.Unlock()
		if items[acct] == nil {
			return nil, errSignedOut
		}
		c := *items[acct]
		return &c, nil
	}
	writeStore = func(acct string, nt *tokens) error {
		mu.Lock()
		defer mu.Unlock()
		c := *nt
		items[acct] = &c
		return nil
	}
	removeStore = func(acct string) error { mu.Lock(); defer mu.Unlock(); delete(items, acct); return nil }
	whoami = func(_ context.Context, _ config, token string) (workspace, error) {
		if strings.HasPrefix(token, "acme") {
			return acme, nil
		}
		return globex, nil
	}
	t.Cleanup(func() { readStore, writeStore, removeStore, whoami = oldR, oldW, oldD, oldWho })
	return items
}

func live() *tokens {
	return &tokens{AccessToken: "acme-a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}
}

func withIndex(t *testing.T, ix workspaceIndex) {
	t.Helper()
	if err := updateIndex(context.Background(), func(cur *workspaceIndex) { *cur = ix }); err != nil {
		t.Fatal(err)
	}
}

func TestIndexPicksTheOnlyOrTheRemembered(t *testing.T) {
	one := workspaceIndex{Workspaces: []workspace{acme}}
	if w := one.pick("/anywhere"); w == nil || w.ID != acme.ID {
		t.Error("one workspace: always it")
	}
	two := workspaceIndex{Workspaces: []workspace{acme, globex}}
	if two.pick("/repo") != nil {
		t.Error("two workspaces, a new repo: ask")
	}
	two.remember("/repo", globex.ID)
	if w := two.pick("/repo"); w == nil || w.ID != globex.ID {
		t.Error("a remembered repo")
	}
	if two.pick("") != nil {
		t.Error("no repo: ask")
	}
	two.remove(globex.ID)
	if len(two.Workspaces) != 1 || len(two.Repos) != 0 {
		t.Errorf("removing a workspace forgets its repos: %+v", two)
	}
	two.add(workspace{ID: acme.ID, Name: "Acme Inc", URLKey: "acme"})
	if len(two.Workspaces) != 1 || two.Workspaces[0].Name != "Acme Inc" {
		t.Errorf("adding a known workspace updates it: %+v", two.Workspaces)
	}
	if two.find("ACME") == nil || two.find("acme inc") == nil || two.find("globex") != nil {
		t.Error("find by URL key or name, any case")
	}
}

func TestReposSettleForTheWorkspace(t *testing.T) {
	cfg := config{Repos: map[string]string{"ENG": "/shared", "acme/ENG": "/acme-eng", "globex/OPS": "/ops"}}
	a := cfg.forWorkspace("acme").Repos
	if a["ENG"] != "/acme-eng" || a["OPS"] != "" || len(a) != 1 {
		t.Errorf("acme: %v", a)
	}
	g := cfg.forWorkspace("globex").Repos
	if g["ENG"] != "/shared" || g["OPS"] != "/ops" || len(g) != 2 {
		t.Errorf("globex: %v", g)
	}
	if cfg.Repos["acme/ENG"] != "/acme-eng" {
		t.Error("the config itself is left alone")
	}
}

func TestRepoKeyIsTheMainCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt := filepath.Join(dir, "repo"), filepath.Join(dir, "wt")
	gitDo := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	gitDo("init", "-q", repo)
	gitDo("-C", repo, "commit", "-q", "--allow-empty", "-m", "x")
	gitDo("-C", repo, "worktree", "add", "-q", wt)
	if err := os.Mkdir(filepath.Join(repo, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{repo, filepath.Join(repo, "sub"), wt} {
		if got := repoKey(d); got != repo {
			t.Errorf("repoKey(%s) = %s, want %s", d, got, repo)
		}
	}
	if got := repoKey(dir); got != dir {
		t.Errorf("outside git: %s", got)
	}
	if repoKey("") != "" {
		t.Error("no directory, no key")
	}
}

func TestLoginAddsTheWorkspace(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{account(acme.ID): live()})
	withIndex(t, workspaceIndex{Workspaces: []workspace{acme}})
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "globex-a", "refresh_token": "globex-r", "expires_in": 3600}
	})
	fakeBrowser(t, func(u *url.URL) { callback(t, url.Values{"code": {"c"}, "state": {u.Query().Get("state")}}) })
	w, err := login(context.Background(), config{ClientID: "cid"}, func(string) {})
	if err != nil || w.ID != globex.ID {
		t.Fatalf("%+v %v", w, err)
	}
	if items[account(globex.ID)] == nil || items[account(globex.ID)].AccessToken != "globex-a" || items[account(acme.ID)] == nil {
		t.Fatalf("stored %v", items)
	}
	ix, _ := readIndex()
	if len(ix.Workspaces) != 2 {
		t.Fatalf("index %+v", ix)
	}
}

func TestAnEarlierSignInMovesToItsWorkspace(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{legacyAccount: live()})
	ix, err := loadWorkspaces(context.Background(), config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Workspaces) != 1 || ix.Workspaces[0] != acme {
		t.Fatalf("index %+v", ix)
	}
	if items[legacyAccount] != nil || items[account(acme.ID)] == nil || items[account(acme.ID)].RefreshToken != "r" {
		t.Fatalf("stored %v", items)
	}
	// And again: nothing left to move.
	if ix, err := loadWorkspaces(context.Background(), config{}); err != nil || len(ix.Workspaces) != 1 {
		t.Fatalf("%+v %v", ix, err)
	}
}

func TestAnEarlierSignInStaysPutWhenLinearCantSay(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{legacyAccount: live()})
	whoami = func(context.Context, config, string) (workspace, error) { return workspace{}, errors.New("offline") }
	if _, err := loadWorkspaces(context.Background(), config{}); err == nil {
		t.Fatal("want the error")
	}
	if items[legacyAccount] == nil {
		t.Fatal("the only sign-in was lost")
	}
}

func TestLogoutEveryWorkspaceKeepsTheOnesThatFail(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{
		account(acme.ID):   live(),
		account(globex.ID): {AccessToken: "globex-a", RefreshToken: "", ExpiresAt: time.Now()}, // can't refresh to revoke
	})
	withIndex(t, workspaceIndex{Workspaces: []workspace{acme, globex}, Repos: map[string]string{"/a": acme.ID, "/g": globex.ID}})
	fakeRevoke(t, 200)
	done, err := logout(context.Background(), config{}, "", false)
	if len(done) != 1 || done[0] != "Acme" || err == nil || !strings.Contains(err.Error(), "Globex") {
		t.Fatalf("done %v err %v", done, err)
	}
	if items[account(acme.ID)] != nil || items[account(globex.ID)] == nil {
		t.Fatalf("stored %v", items)
	}
	ix, _ := readIndex()
	if len(ix.Workspaces) != 1 || ix.Workspaces[0].ID != globex.ID || ix.Repos["/a"] != "" || ix.Repos["/g"] != globex.ID {
		t.Fatalf("index %+v", ix)
	}
}

func TestLogoutOneWorkspace(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{account(acme.ID): live(), account(globex.ID): live()})
	withIndex(t, workspaceIndex{Workspaces: []workspace{acme, globex}})
	fakeRevoke(t, 200)
	if done, err := logout(context.Background(), config{}, "globex", false); err != nil || len(done) != 1 {
		t.Fatalf("%v %v", done, err)
	}
	if items[account(acme.ID)] == nil || items[account(globex.ID)] != nil {
		t.Fatalf("stored %v", items)
	}
	if _, err := logout(context.Background(), config{}, "initech", false); err == nil {
		t.Fatal("an unknown workspace")
	}
}

func TestLogoutAnEarlierSignIn(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{legacyAccount: live()})
	fakeRevoke(t, 200)
	if done, err := logout(context.Background(), config{}, "", false); err != nil || len(done) != 1 || items[legacyAccount] != nil {
		t.Fatalf("%v %v %v", done, err, items)
	}
	if _, err := logout(context.Background(), config{}, "", false); !errors.Is(err, errNotSignedIn) {
		t.Fatalf("%v", err)
	}
}

// ── the picker ────────────────────────────────────────────────────────────────

func pickerIn(t *testing.T, repo string, ix workspaceIndex) model {
	t.Helper()
	m := newModel(context.Background(), config{Repos: map[string]string{"globex/OPS": "/ops"}}, repo)
	m.repo, m.width, m.height = repo, 100, 30
	next, _ := m.Update(workspacesMsg{index: ix})
	return next.(model)
}

func TestPickerAsksOncePerRepo(t *testing.T) {
	fakeStores(t, nil)
	two := workspaceIndex{Workspaces: []workspace{acme, globex}}
	withIndex(t, two)
	m := pickerIn(t, "/repo", two)
	if m.mode != modeChooseWorkspace || !strings.Contains(screenText(m), "Which Linear workspace is repo in?") {
		t.Fatalf("mode %v\n%s", m.mode, screenText(m))
	}
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	next, cmd := next.(model).handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.ws == nil || m.ws.ID != globex.ID || m.mode != modeLoading || m.cfg.Repos["OPS"] != "/ops" {
		t.Fatalf("ws %v mode %v repos %v", m.ws, m.mode, m.cfg.Repos)
	}
	if c, ok := m.client.(*linearClient); !ok || c.ws.ID != globex.ID {
		t.Fatalf("client %#v", m.client)
	}
	runCmds(cmd) // saves the choice
	ix, _ := readIndex()
	if ix.Repos["/repo"] != globex.ID {
		t.Fatalf("not remembered: %+v", ix)
	}
	if m := pickerIn(t, "/repo", ix); m.ws == nil || m.ws.ID != globex.ID {
		t.Fatal("asked again")
	}
}

func TestPickerWithOneWorkspaceDoesntAsk(t *testing.T) {
	m := pickerIn(t, "/repo", workspaceIndex{Workspaces: []workspace{acme}})
	if m.ws == nil || m.ws.ID != acme.ID || m.mode != modeLoading {
		t.Fatalf("ws %v mode %v", m.ws, m.mode)
	}
	if strings.Contains(screenText(m), "Acme") || strings.Contains(screenText(m), "workspace") {
		t.Errorf("one workspace needs no name or ^t:\n%s", screenText(m))
	}
}

func TestPickerWithNoWorkspaceSignsIn(t *testing.T) {
	if m := pickerIn(t, "/repo", workspaceIndex{}); m.mode != modeSignedOut {
		t.Fatalf("mode %v", m.mode)
	}
}

func TestCtrlTSwitchesAndDropsTheOldWorkspacesReplies(t *testing.T) {
	fakeStores(t, nil)
	two := workspaceIndex{Workspaces: []workspace{acme, globex}, Repos: map[string]string{"/repo": acme.ID}}
	withIndex(t, two)
	m := pickerIn(t, "/repo", two)
	old := m.gen
	next, _ := m.Update(issuesMsg{issues: []issue{{ID: "1", Identifier: "ACME-1", Title: "a"}}, gen: old})
	m = next.(model)
	if !strings.Contains(screenText(m), "ACME-1") || !strings.Contains(screenText(m), "Acme") || !strings.Contains(screenText(m), "^t workspace") {
		t.Fatalf("\n%s", screenText(m))
	}

	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = next.(model)
	if m.mode != modeChooseWorkspace || m.wsCursor != 0 {
		t.Fatalf("mode %v cursor %d", m.mode, m.wsCursor)
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc}) // changed your mind
	if m2 := next.(model); m2.mode != modeList || m2.ws.ID != acme.ID {
		t.Fatalf("esc: mode %v", m2.mode)
	}

	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	next, _ = next.(model).handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.ws.ID != globex.ID || len(m.issues) != 0 || m.index.Repos["/repo"] != globex.ID {
		t.Fatalf("ws %v issues %d repos %v", m.ws, len(m.issues), m.index.Repos)
	}
	// Acme's load, still in flight when you switched, lands late.
	next, _ = m.Update(issuesMsg{issues: []issue{{ID: "2", Identifier: "ACME-2", Title: "late"}}, gen: old})
	if m := next.(model); len(m.issues) != 0 {
		t.Fatal("the old workspace's issues shown as the new one's")
	}
}

func TestSigningInFromTheWorkspaceScreenRemembersIt(t *testing.T) {
	fakeStores(t, nil)
	two := workspaceIndex{Workspaces: []workspace{acme}}
	withIndex(t, two)
	m := pickerIn(t, "/repo", two)
	next, _ := m.Update(loginDoneMsg{ws: globex})
	m = next.(model)
	if m.ws.ID != globex.ID || len(m.index.Workspaces) != 2 || m.index.Repos["/repo"] != globex.ID {
		t.Fatalf("ws %v index %+v", m.ws, m.index)
	}
}

// runCmds runs a command and any batch inside it, for their side effects.
func runCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmds(c)
		}
	}
}
