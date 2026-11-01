package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestBranchRefs(t *testing.T) {
	for branch, want := range map[string]string{
		"mrolafsson/ops-20-capability-registry": "OPS-20",
		"ENG-142":                               "ENG-142",
		"eng-142-fix-login":                     "ENG-142",
		"feature/ENG-142_fix":                   "ENG-142",
		"fix-utf-8-in-eng-7":                    "UTF-8 ENG-7",
		"release-2026-10":                       "RELEASE-2026", // nobody's team: the caller's to drop
		"eng-142abc":                            "",
		"xeng-0":                                "",
		"main":                                  "",
		"":                                      "",
		"a1-2-b-3":                              "A1-2 B-3",
	} {
		var got []string
		for _, r := range branchRefs(branch) {
			got = append(got, r.String())
		}
		if strings.Join(got, " ") != want {
			t.Errorf("%q: %v, want %q", branch, got, want)
		}
	}
}

// labelWorld stands in herdr, git and Linear for a tick.
type labelWorld struct {
	spaces   []workspaceInfo
	panes    []paneInfo
	branches map[string]string // dir → branch
	repos    map[string]string // dir → repo
	index    workspaceIndex
	teams    map[string][]string // Linear workspace ID → team keys
	issues   map[string]map[issueRef]taggedIssue
	down     bool // Linear doesn't answer
	now      time.Time
	asked    []string // the lookups made
	sent     map[string]map[string]any
}

func space(id, dir string, tokens map[string]string) workspaceInfo {
	w := workspaceInfo{WorkspaceID: id, Tokens: tokens}
	if dir != "" {
		w.Worktree = &struct {
			CheckoutPath string `json:"checkout_path"`
		}{dir}
	}
	return w
}

func newLabelWorld(t *testing.T) *labelWorld {
	t.Helper()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	w := &labelWorld{
		branches: map[string]string{}, repos: map[string]string{}, teams: map[string][]string{},
		issues: map[string]map[issueRef]taggedIssue{}, sent: map[string]map[string]any{},
		now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC),
	}
	old := []any{listWorkspacesFn, listPanesFn, reportFn, signedInFn, branchFn, repoKeyFn, teamKeysFn, issueLookupFn, nowFn}
	listWorkspacesFn = func() ([]workspaceInfo, error) { return w.spaces, nil }
	listPanesFn = func() ([]paneInfo, error) { return w.panes, nil }
	reportFn = func(method string, params map[string]any) error {
		id, _ := params["pane_id"].(string)
		if id == "" {
			id, _ = params["workspace_id"].(string)
		}
		tokens := params["tokens"].(map[string]any)
		w.sent[id] = tokens
		// herdr shows what it's told.
		show := func(cur map[string]string) map[string]string {
			out := map[string]string{}
			for k, v := range cur {
				out[k] = v
			}
			for k, v := range tokens {
				if v == nil {
					delete(out, k)
				} else {
					out[k] = v.(string)
				}
			}
			return out
		}
		for i := range w.spaces {
			if w.spaces[i].WorkspaceID == id {
				w.spaces[i].Tokens = show(w.spaces[i].Tokens)
			}
		}
		for i := range w.panes {
			if w.panes[i].PaneID == id {
				w.panes[i].Tokens = show(w.panes[i].Tokens)
			}
		}
		return nil
	}
	signedInFn = func(context.Context, config) (workspaceIndex, error) { return w.index, nil }
	branchFn = func(_ context.Context, dir string) string { return w.branches[dir] }
	repoKeyFn = func(dir string) string { return w.repos[dir] }
	teamKeysFn = func(_ context.Context, _ config, lw workspace) ([]string, error) {
		if w.down {
			return nil, errors.New("offline")
		}
		w.asked = append(w.asked, "teams "+lw.ID)
		return w.teams[lw.ID], nil
	}
	issueLookupFn = func(_ context.Context, _ config, lw workspace, refs []issueRef) (map[issueRef]taggedIssue, error) {
		if w.down {
			return nil, errors.New("offline")
		}
		names := make([]string, len(refs))
		for i, r := range refs {
			names[i] = r.String()
		}
		sort.Strings(names)
		w.asked = append(w.asked, "issues "+lw.ID+" "+strings.Join(names, ","))
		return w.issues[lw.ID], nil
	}
	nowFn = func() time.Time { return w.now }
	t.Cleanup(func() {
		listWorkspacesFn, listPanesFn = old[0].(func() ([]workspaceInfo, error)), old[1].(func() ([]paneInfo, error))
		reportFn = old[2].(func(string, map[string]any) error)
		signedInFn = old[3].(func(context.Context, config) (workspaceIndex, error))
		branchFn, repoKeyFn = old[4].(func(context.Context, string) string), old[5].(func(string) string)
		teamKeysFn = old[6].(func(context.Context, config, workspace) ([]string, error))
		issueLookupFn = old[7].(func(context.Context, config, workspace, []issueRef) (map[issueRef]taggedIssue, error))
		nowFn = old[8].(func() time.Time)
	})
	return w
}

func (w *labelWorld) tick(t *testing.T, force bool) {
	t.Helper()
	w.asked, w.sent = nil, map[string]map[string]any{}
	if err := tick(context.Background(), withDefaults(config{}), force); err != nil && !w.down {
		t.Fatal(err)
	}
	w.now = w.now.Add(10 * time.Second) // past the cooldown
}

func TestTickTagsSpacesAndAgentsWithTheirBranchsIssue(t *testing.T) {
	w := newLabelWorld(t)
	w.index = workspaceIndex{Workspaces: []workspace{{ID: "L1", URLKey: "acme"}}}
	w.teams["L1"] = []string{"ENG", "WEB"}
	w.issues["L1"] = map[issueRef]taggedIssue{
		{"ENG", 142}: {Identifier: "ENG-142", Title: "Fix   the login\nredirect", State: "In Progress"},
	}
	w.spaces = []workspaceInfo{space("w1", "/wt/eng-142", nil), space("w2", "/repo", nil), space("w3", "", nil)}
	w.panes = []paneInfo{
		{PaneID: "w1:p1", WorkspaceID: "w1", Agent: "claude", Cwd: "/wt/eng-142"},
		{PaneID: "w1:p2", WorkspaceID: "w1"}, // a shell
		{PaneID: "w2:p1", WorkspaceID: "w2", Agent: "claude", Cwd: "/repo"},
		{PaneID: "w2:p2", WorkspaceID: "w2", Agent: "codex", Cwd: "/wt/eng-142"}, // started elsewhere
		{PaneID: "w3:p1", WorkspaceID: "w3", Agent: "claude", Cwd: "/wt/utf"},
	}
	w.branches = map[string]string{"/wt/eng-142": "me/eng-142-fix-login", "/repo": "main", "/wt/utf": "fix-utf-8"}
	w.tick(t, false)

	if got := fmt.Sprint(w.asked); got != "[teams L1 issues L1 ENG-142]" {
		t.Errorf("asked %s", got)
	}
	want := map[string]string{"issue": "ENG-142", "issue_state": "In Progress", "issue_title": "Fix the login redirect"}
	for _, id := range []string{"w1", "w1:p1", "w2:p2"} {
		for k, v := range want {
			if w.sent[id][k] != v {
				t.Errorf("%s: %s = %v", id, k, w.sent[id][k])
			}
		}
	}
	for _, id := range []string{"w1:p2", "w2", "w2:p1", "w3", "w3:p1"} {
		if _, ok := w.sent[id]; ok {
			t.Errorf("%s was tagged: %v", id, w.sent[id])
		}
	}

	// Nothing's changed and nothing's due: neither Linear nor herdr hears of it.
	w.tick(t, false)
	if len(w.asked) != 0 || len(w.sent) != 0 {
		t.Errorf("asked %v, sent %v", w.asked, w.sent)
	}

	// The issue moves on; once its answer is old enough, so do the tokens.
	w.issues["L1"][issueRef{"ENG", 142}] = taggedIssue{Identifier: "ENG-142", Title: "Fix the login redirect", State: "In Review"}
	w.now = w.now.Add(3 * time.Minute)
	w.tick(t, false)
	if w.sent["w1:p1"]["issue_state"] != "In Review" {
		t.Errorf("sent %v", w.sent)
	}

	// The space leaves the branch: its tokens are cleared, the agents' with it.
	w.branches["/wt/eng-142"] = "main"
	w.tick(t, false)
	for _, id := range []string{"w1", "w1:p1", "w2:p2"} {
		if v, ok := w.sent[id]["issue"]; !ok || v != nil {
			t.Errorf("%s not cleared: %v", id, w.sent[id])
		}
	}
}

func TestTickLeavesWhatsShownWhenLinearCantSay(t *testing.T) {
	w := newLabelWorld(t)
	w.index = workspaceIndex{Workspaces: []workspace{{ID: "L1"}}}
	w.teams["L1"] = []string{"ENG"}
	w.issues["L1"] = map[issueRef]taggedIssue{{"ENG", 1}: {Identifier: "ENG-1", Title: "One", State: "Todo"}}
	w.spaces = []workspaceInfo{space("w1", "/a", nil)}
	w.branches["/a"] = "eng-1"
	w.tick(t, false)
	if w.sent["w1"]["issue"] != "ENG-1" {
		t.Fatalf("sent %v", w.sent)
	}
	// Offline, and the answer's due: what's shown stays.
	w.down = true
	w.now = w.now.Add(10 * time.Minute)
	w.tick(t, false)
	if len(w.sent) != 0 {
		t.Errorf("sent %v", w.sent)
	}
	// An issue Linear doesn't have isn't one.
	w.down = false
	w.branches["/a"] = "eng-999"
	w.now = w.now.Add(10 * time.Minute)
	w.tick(t, false)
	if v, ok := w.sent["w1"]["issue"]; !ok || v != nil {
		t.Errorf("not cleared: %v", w.sent)
	}
}

func TestTickPicksTheLinearWorkspaceByRepoOrByTeam(t *testing.T) {
	w := newLabelWorld(t)
	w.index = workspaceIndex{
		Workspaces: []workspace{{ID: "L1"}, {ID: "L2"}},
		Repos:      map[string]string{"/given": "L2"},
	}
	w.teams["L1"], w.teams["L2"] = []string{"ENG", "OPS"}, []string{"ENG", "WEB"}
	w.issues["L1"] = map[issueRef]taggedIssue{{"OPS", 3}: {Identifier: "OPS-3", State: "Todo"}, {"ENG", 5}: {Identifier: "ENG-5", State: "L1's"}}
	w.issues["L2"] = map[issueRef]taggedIssue{{"ENG", 5}: {Identifier: "ENG-5", State: "L2's"}}
	w.spaces = []workspaceInfo{space("w1", "/a", nil), space("w2", "/b", nil), space("w3", "/c", nil), space("w4", "/d", nil)}
	w.branches = map[string]string{"/a": "ops-3", "/b": "eng-5", "/c": "eng-5", "/d": "ops-3"}
	w.repos = map[string]string{"/a": "/free", "/b": "/free", "/c": "/given", "/d": "/given"}
	w.tick(t, false)
	if w.sent["w1"]["issue"] != "OPS-3" {
		t.Errorf("the only workspace with the team: %v", w.sent["w1"])
	}
	if _, ok := w.sent["w2"]; ok {
		t.Errorf("two workspaces have the team, and the repo is neither's: %v", w.sent["w2"])
	}
	if w.sent["w3"]["issue_state"] != "L2's" {
		t.Errorf("the repo's workspace: %v", w.sent["w3"])
	}
	if _, ok := w.sent["w4"]; ok {
		t.Errorf("the repo's workspace has no such team: %v", w.sent["w4"])
	}
}

func TestTickClearsEverythingWhenSignedOutOrTurnedOff(t *testing.T) {
	w := newLabelWorld(t)
	w.spaces = []workspaceInfo{space("w1", "/a", map[string]string{"issue": "ENG-1", "issue_state": "Todo", "pr": "#4"})}
	w.panes = []paneInfo{{PaneID: "w1:p1", WorkspaceID: "w1", Agent: "claude", Tokens: map[string]string{"issue": "ENG-1"}}}
	w.tick(t, false) // nobody's signed in
	for _, id := range []string{"w1", "w1:p1"} {
		if v, ok := w.sent[id]["issue"]; !ok || v != nil {
			t.Errorf("%s not cleared: %v", id, w.sent[id])
		}
		if _, ok := w.sent[id]["pr"]; ok {
			t.Errorf("%s: someone else's token was touched", id)
		}
	}
	if len(w.asked) != 0 {
		t.Errorf("asked %v", w.asked)
	}
}
