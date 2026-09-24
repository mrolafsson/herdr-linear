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
// answers whoami from the access token: "acme-…" is Acme, "dead-…" revoked,
// anything else Globex.
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
		switch {
		case strings.HasPrefix(token, "acme"):
			return acme, nil
		case strings.HasPrefix(token, "dead"):
			return workspace{}, errSignedOut
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
	found := func(q string) bool { w, err := two.find(q); return err == nil && w != nil }
	if !found("ACME") || !found("acme inc") || found("globex") {
		t.Error("find by URL key or name, any case")
	}
	two.add(workspace{ID: "org-2", Name: "Acme Inc", URLKey: "acme-2"})
	if found("acme inc") || !found("acme-2") {
		t.Error("a name two workspaces share is ambiguous; URL keys aren't")
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

// ── review round 1 ────────────────────────────────────────────────────────────

func TestAClientUsesItsOwnWorkspacesTokens(t *testing.T) {
	fakeStores(t, map[string]*tokens{account(globex.ID): live()})
	c := &linearClient{ws: acme}
	if _, err := c.myIssues(context.Background()); !errors.Is(err, errSignedOut) {
		t.Fatalf("Acme's client got past with Globex's tokens: %v", err)
	}
}

func TestMigrationReidentifiesTokensThatChanged(t *testing.T) {
	// An old version signs in to Globex while the move is identifying Acme's token.
	items := fakeStores(t, map[string]*tokens{legacyAccount: live()})
	calls := 0
	whoami = func(_ context.Context, _ config, token string) (workspace, error) {
		calls++
		if calls == 1 {
			_ = writeStore(legacyAccount, &tokens{AccessToken: "globex-a", RefreshToken: "gr", ExpiresAt: time.Now().Add(time.Hour)})
			return acme, nil
		}
		if strings.HasPrefix(token, "acme") {
			return acme, nil
		}
		return globex, nil
	}
	ix, err := loadWorkspaces(context.Background(), config{})
	if err != nil || len(ix.Workspaces) != 1 || ix.Workspaces[0] != globex {
		t.Fatalf("%+v %v", ix, err)
	}
	if items[account(acme.ID)] != nil || items[account(globex.ID)] == nil || items[account(globex.ID)].RefreshToken != "gr" {
		t.Fatalf("stored %v", items)
	}
}

func TestMigrationKeepsANewerSignIn(t *testing.T) {
	newer := &tokens{AccessToken: "acme-new", RefreshToken: "new", ExpiresAt: time.Now().Add(2 * time.Hour)}
	items := fakeStores(t, map[string]*tokens{legacyAccount: live(), account(acme.ID): newer})
	withIndex(t, workspaceIndex{Workspaces: []workspace{acme}})
	seen := fakeRevoke(t, 200)
	if _, err := loadWorkspaces(context.Background(), config{}); err != nil {
		t.Fatal(err)
	}
	if items[account(acme.ID)].RefreshToken != "new" || items[legacyAccount] != nil || len(*seen) != 0 {
		t.Fatalf("stored %v revokes %d", items, len(*seen))
	}
}

func TestAFailedMigrationDoesntHideTheOtherWorkspaces(t *testing.T) {
	fakeStores(t, map[string]*tokens{legacyAccount: live(), account(globex.ID): live()})
	withIndex(t, workspaceIndex{Workspaces: []workspace{globex}})
	whoami = func(context.Context, config, string) (workspace, error) { return workspace{}, errors.New("offline") }
	ix, err := loadWorkspaces(context.Background(), config{})
	if err == nil || len(ix.Workspaces) != 1 {
		t.Fatalf("%+v %v", ix, err)
	}
	m := newModel(context.Background(), config{}, "/repo")
	m.width, m.height = 100, 30
	next, _ := m.Update(workspacesMsg{ix, err})
	m = next.(model)
	if m.ws == nil || m.ws.ID != globex.ID || !strings.Contains(m.err, "offline") {
		t.Fatalf("ws %v err %q", m.ws, m.err)
	}
}

func TestABrokenIndexIsSetAside(t *testing.T) {
	fakeStores(t, nil)
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath(), []byte(`{"workspaces": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ix, err := readIndex(); err != nil || len(ix.Workspaces) != 0 {
		t.Fatalf("%+v %v", ix, err)
	}
	if _, err := os.Stat(indexPath() + ".broken"); err != nil {
		t.Fatal("not kept for a look")
	}
	if err := updateIndex(context.Background(), func(ix *workspaceIndex) { ix.add(acme) }); err != nil {
		t.Fatal("stuck on it:", err)
	}
}

func TestLogoutUnlistsAWorkspaceWithNoTokens(t *testing.T) {
	fakeStores(t, map[string]*tokens{account(acme.ID): live()})
	withIndex(t, workspaceIndex{Workspaces: []workspace{acme, globex}, Repos: map[string]string{"/g": globex.ID}})
	fakeRevoke(t, 200)
	done, err := logout(context.Background(), config{}, "", false)
	if err != nil || len(done) != 1 {
		t.Fatalf("%v %v", done, err)
	}
	if ix, _ := readIndex(); len(ix.Workspaces) != 0 || len(ix.Repos) != 0 {
		t.Fatalf("index %+v", ix)
	}
}

func TestLogoutArgs(t *testing.T) {
	for _, c := range []struct {
		args  []string
		local bool
		which string
		ok    bool
	}{
		{nil, false, "", true},
		{[]string{"--local"}, true, "", true},
		{[]string{"--local", "acme"}, true, "acme", true},
		{[]string{"acme", "--local"}, true, "acme", true},
		{[]string{"acme", "globex"}, false, "", false},
		{[]string{"--force"}, false, "", false},
	} {
		local, which, err := logoutArgs(c.args)
		if (err == nil) != c.ok || (c.ok && (local != c.local || which != c.which)) {
			t.Errorf("%v: %v %q %v", c.args, local, which, err)
		}
	}
}

func TestSwitchingDropsTheOldWorkspacesDetailAndMarks(t *testing.T) {
	fakeStores(t, nil)
	two := workspaceIndex{Workspaces: []workspace{acme, globex}, Repos: map[string]string{"/repo": acme.ID}}
	withIndex(t, two)
	m := pickerIn(t, "/repo", two)
	old := m.gen
	is := issue{ID: "1", Identifier: "ACME-1", Title: "a"}
	next, _ := m.Update(issuesMsg{issues: []issue{is}, gen: old})
	next, _ = next.(model).openIssue(is) // its detail load goes out
	m = next.(model)
	m.screen = screenList                  // back to the list before it lands
	next, _ = m.useWorkspace(globex, true) // and on to Globex
	next, _ = next.(model).Update(issueDetailMsg{id: "1", err: errSignedOut, gen: old})
	next, _ = next.(model).Update(worktreesMsg{branches: map[string]bool{"acme-branch": true}, gen: old})
	next, _ = next.(model).Update(statesMsg{teamID: "t", err: errSignedOut, gen: old})
	m = next.(model)
	if m.mode == modeSignedOut || m.worktrees["acme-branch"] || m.cur != nil {
		t.Fatalf("mode %v worktrees %v cur %v", m.mode, m.worktrees, m.cur)
	}
}

func TestSignedOutOfOneWorkspaceCanPickAnother(t *testing.T) {
	fakeStores(t, nil)
	two := workspaceIndex{Workspaces: []workspace{acme, globex}, Repos: map[string]string{"/repo": acme.ID}}
	withIndex(t, two)
	m := pickerIn(t, "/repo", two)
	next, _ := m.Update(issuesMsg{err: errSignedOut, gen: m.gen})
	m = next.(model)
	if m.mode != modeSignedOut || !strings.Contains(screenText(m), "signed out of Acme") {
		t.Fatalf("mode %v\n%s", m.mode, screenText(m))
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlT})
	if m := next.(model); m.mode != modeChooseWorkspace {
		t.Fatalf("mode %v", m.mode)
	}
}

func TestRepoKeyResolvesSymlinks(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	real, link := filepath.Join(dir, "real"), filepath.Join(dir, "link")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if repoKey(link) != repoKey(real) {
		t.Errorf("%s vs %s", repoKey(link), repoKey(real))
	}
}

// ── review round 2 ────────────────────────────────────────────────────────────

func TestMigrationReplacesTokensLinearRefuses(t *testing.T) {
	// A move that wrote the copy but couldn't delete the old item; since
	// then the old item was refreshed, so the copy's refresh token is spent.
	// Its expiry says nothing: it's Linear's answer that counts.
	spent := &tokens{AccessToken: "dead-a", RefreshToken: "spent", ExpiresAt: time.Now().Add(3 * time.Hour)}
	items := fakeStores(t, map[string]*tokens{legacyAccount: live(), account(acme.ID): spent})
	if _, err := loadWorkspaces(context.Background(), config{}); err != nil {
		t.Fatal(err)
	}
	if items[account(acme.ID)].RefreshToken != "r" || items[legacyAccount] != nil {
		t.Fatalf("stored %v", items)
	}
}

func TestMigrationChangesNothingWhenLinearCantSayWhichWorks(t *testing.T) {
	other := &tokens{AccessToken: "acme-other", RefreshToken: "other", ExpiresAt: time.Now().Add(time.Hour)}
	items := fakeStores(t, map[string]*tokens{legacyAccount: live(), account(acme.ID): other})
	whoami = func(_ context.Context, _ config, token string) (workspace, error) {
		if token == "acme-other" {
			return workspace{}, errors.New("offline")
		}
		return acme, nil
	}
	if _, err := loadWorkspaces(context.Background(), config{}); err == nil {
		t.Fatal("want the error")
	}
	if items[legacyAccount] == nil || items[account(acme.ID)].RefreshToken != "other" {
		t.Fatalf("a pair was dropped on a guess: %v", items)
	}
}

func TestLogoutAllTakesTheOldSignInAndTheListedOnes(t *testing.T) {
	items := fakeStores(t, map[string]*tokens{legacyAccount: live(), account(globex.ID): live()})
	withIndex(t, workspaceIndex{Workspaces: []workspace{globex}})
	fakeRevoke(t, 200)
	done, err := logout(context.Background(), config{}, "", false)
	if err != nil || len(done) != 2 || len(items) != 0 {
		t.Fatalf("done %v err %v stored %v", done, err, items)
	}
}

func TestLoginListsTheWorkspaceBeforeStoringItsTokens(t *testing.T) {
	fakeStores(t, nil)
	writeStore = func(string, *tokens) error { return errors.New("keychain locked") }
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "acme-a", "refresh_token": "r", "expires_in": 3600}
	})
	fakeBrowser(t, func(u *url.URL) { callback(t, url.Values{"code": {"c"}, "state": {u.Query().Get("state")}}) })
	if _, err := login(context.Background(), config{ClientID: "cid"}, func(string) {}); err == nil {
		t.Fatal("want the keychain error")
	}
	// Listed without tokens: it asks you to sign in again; nothing is hidden.
	if ix, _ := readIndex(); len(ix.Workspaces) != 1 {
		t.Fatalf("index %+v", ix)
	}
}

func TestALoadLandingMidActionDoesntEndIt(t *testing.T) {
	m := pickerIn(t, "/repo", workspaceIndex{Workspaces: []workspace{acme}})
	m.mode = modeBusy
	for _, msg := range []tea.Msg{
		projectsMsg{gen: m.gen},
		issuesMsg{gen: m.gen},
		projectsMsg{err: errSignedOut, gen: m.gen},
	} {
		next, _ := m.Update(msg)
		if m2 := next.(model); m2.mode != modeBusy {
			t.Fatalf("%T took the picker out of an action: mode %v", msg, m2.mode)
		}
	}
}
