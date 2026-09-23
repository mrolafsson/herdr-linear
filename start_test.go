package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeLinearSource records what start did to Linear, in order.
type fakeLinearSource struct {
	*demoSource // the methods start doesn't use; never called
	fresh       issue
	freshErr    error
	startErr    error
	calls       *[]string
}

func (f fakeLinearSource) freshIssue(context.Context, string) (issue, error) {
	*f.calls = append(*f.calls, "fresh")
	return f.fresh, f.freshErr
}

func (f fakeLinearSource) startIssue(_ context.Context, is issue) error {
	*f.calls = append(*f.calls, "linear:"+is.State.Name)
	return f.startErr
}

type startRig struct {
	calls   []string
	created bool
	rootID  string
	treeErr error
	prompt  string
}

// rig swaps the herdr side of doWorktree for recorders.
func rig(t *testing.T) *startRig {
	t.Helper()
	r := &startRig{created: true, rootID: "w9:p1"}
	oldO, oldK := openWorktreeFn, spawnKickoffFn
	openWorktreeFn = func(_ context.Context, _ config, tg target, _ string) (*worktreeResult, bool, error) {
		r.calls = append(r.calls, "worktree:"+tg.Branch)
		if r.treeErr != nil {
			return nil, false, r.treeErr
		}
		res := &worktreeResult{Workspace: workspaceInfo{WorkspaceID: "w9"}}
		if r.rootID != "" {
			res.RootPane = &paneInfo{PaneID: r.rootID}
		}
		return res, r.created, nil
	}
	spawnKickoffFn = func(ws, pane, text string) error {
		r.calls = append(r.calls, "prompt:"+pane)
		r.prompt = text
		return nil
	}
	t.Cleanup(func() { openWorktreeFn, spawnKickoffFn = oldO, oldK })
	return r
}

func todo(id string) issue {
	is := mkIssue(id, "unstarted", "Todo", 1, 0)
	is.ID, is.BranchName, is.Team = "id-"+id, "b-"+id, team{ID: "t", Key: "ENG"}
	return is
}

func startWith(r *startRig, src fakeLinearSource, loaded issue) actionDoneMsg {
	src.calls = &r.calls
	return doWorktree(context.Background(), config{StartPrompt: "/ticket {identifier}"}, src, "/repo", issueTarget(loaded), &loaded, true)
}

func TestStartOrderWorktreeBeforeLinearBeforePrompt(t *testing.T) {
	r := rig(t)
	msg := startWith(r, fakeLinearSource{fresh: todo("ENG-1")}, todo("ENG-1"))
	if msg.err != nil || msg.note != "" {
		t.Fatalf("%+v", msg)
	}
	if got := strings.Join(r.calls, " "); got != "fresh worktree:b-ENG-1 linear:Todo prompt:w9:p1" {
		t.Fatalf("order: %s", got)
	}
	if r.prompt != "/ticket ENG-1" {
		t.Fatalf("prompt %q", r.prompt)
	}
}

func TestStartStopsBeforeChangingAnythingWhenTheIssueMoved(t *testing.T) {
	cases := map[string]func(*issue){
		"closed since loading":   func(f *issue) { f.State = workflowState{Name: "Done", Type: "completed"} },
		"canceled since loading": func(f *issue) { f.State = workflowState{Name: "Canceled", Type: "canceled"} },
		"taken since loading":    func(f *issue) { f.Assignee = &person{ID: "u2", Name: "Ada"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := rig(t)
			fresh := todo("ENG-2")
			change(&fresh)
			msg := startWith(r, fakeLinearSource{fresh: fresh}, todo("ENG-2"))
			if msg.err == nil || !strings.Contains(msg.err.Error(), "wasn't started") {
				t.Fatalf("want a refusal, got %+v", msg)
			}
			if got := strings.Join(r.calls, " "); got != "fresh" {
				t.Fatalf("changed things anyway: %s", got)
			}
		})
	}
}

func TestStartKeepsSomeoneElsesIssueTheirsIfItWasTheirsAlready(t *testing.T) {
	r := rig(t)
	loaded := todo("ENG-3")
	loaded.Assignee = &person{ID: "u2", Name: "Ada"}
	fresh := loaded
	msg := startWith(r, fakeLinearSource{fresh: fresh}, loaded)
	if msg.err != nil {
		t.Fatalf("an issue that was already Ada's may be started: %v", msg.err)
	}
}

func TestFailedWorktreeLeavesLinearUntouched(t *testing.T) {
	r := rig(t)
	r.treeErr = errors.New("not a git repository")
	msg := startWith(r, fakeLinearSource{fresh: todo("ENG-4")}, todo("ENG-4"))
	if msg.err == nil {
		t.Fatal("want the worktree error")
	}
	for _, c := range r.calls {
		if strings.HasPrefix(c, "linear:") || strings.HasPrefix(c, "prompt:") {
			t.Fatalf("acted after the worktree failed: %v", r.calls)
		}
	}
}

func TestLinearRefusalMeansNoPrompt(t *testing.T) {
	r := rig(t)
	msg := startWith(r, fakeLinearSource{fresh: todo("ENG-5"), startErr: errors.New("forbidden")}, todo("ENG-5"))
	if !strings.Contains(msg.note, "Linear wasn't updated") || strings.Contains(strings.Join(r.calls, " "), "prompt:") {
		t.Fatalf("note %q calls %v", msg.note, r.calls)
	}
}

func TestNoPromptIntoAnExistingWorktreeOrAnUnknownPane(t *testing.T) {
	r := rig(t)
	r.created = false
	if msg := startWith(r, fakeLinearSource{fresh: todo("ENG-6")}, todo("ENG-6")); !strings.Contains(msg.note, "already existed") {
		t.Fatalf("%+v", msg)
	}
	r2 := rig(t)
	r2.rootID = ""
	if msg := startWith(r2, fakeLinearSource{fresh: todo("ENG-7")}, todo("ENG-7")); !strings.Contains(msg.note, "no prompt was sent") {
		t.Fatalf("%+v", msg)
	}
	for _, calls := range [][]string{r.calls, r2.calls} {
		if strings.Contains(strings.Join(calls, " "), "prompt:") {
			t.Fatalf("prompted anyway: %v", calls)
		}
	}
}

func TestPromptUsesTheFreshIssue(t *testing.T) {
	r := rig(t)
	fresh := todo("ENG-8")
	fresh.Title = "renamed since loading"
	src := fakeLinearSource{fresh: fresh}
	src.calls = &r.calls
	loaded := todo("ENG-8")
	doWorktree(context.Background(), config{StartPrompt: "{identifier}: {title}"}, src, "/repo", issueTarget(loaded), &loaded, true)
	if r.prompt != "ENG-8: renamed since loading" {
		t.Fatalf("prompt %q", r.prompt)
	}
}
