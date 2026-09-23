package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeLinearSource records what start did to Linear, in order. Each re-read
// returns the next of reads (the last one repeats), so a test can change the
// issue between start's first check and its second.
type fakeLinearSource struct {
	*demoSource // the methods start doesn't use; never called
	fresh       issue
	reads       *[]issue
	freshErr    error
	startErr    error
	calls       *[]string
}

func (f fakeLinearSource) freshIssue(context.Context, string) (issue, error) {
	*f.calls = append(*f.calls, "fresh")
	if f.reads != nil && len(*f.reads) > 0 {
		next := (*f.reads)[0]
		if len(*f.reads) > 1 {
			*f.reads = (*f.reads)[1:]
		}
		return next, f.freshErr
	}
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
	if got := strings.Join(r.calls, " "); got != "fresh worktree:b-ENG-1 fresh linear:Todo prompt:w9:p1" {
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

// Start claims work and sets your agent on it: never on a coworker's issue,
// even one that was already theirs when the picker loaded.
func TestStartRefusesACoworkersIssue(t *testing.T) {
	r := rig(t)
	loaded := todo("ENG-3")
	loaded.Assignee = &person{ID: "u2", Name: "Ada"}
	msg := startWith(r, fakeLinearSource{fresh: loaded}, loaded)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "assigned to Ada") {
		t.Fatalf("want a refusal, got %+v", msg)
	}
	if got := strings.Join(r.calls, " "); got != "fresh" {
		t.Fatalf("acted anyway: %s", got)
	}
}

// Round 2: making the worktree takes time; the issue can change meanwhile.
func TestChangeDuringWorktreeCreationStopsTheLinearUpdate(t *testing.T) {
	for name, change := range map[string]func(*issue){
		"taken":  func(f *issue) { f.Assignee = &person{ID: "u2", Name: "Ada"} },
		"closed": func(f *issue) { f.State = workflowState{Name: "Done", Type: "completed"} },
	} {
		t.Run(name, func(t *testing.T) {
			r := rig(t)
			later := todo("ENG-9")
			change(&later)
			reads := []issue{todo("ENG-9"), later}
			msg := startWith(r, fakeLinearSource{reads: &reads}, todo("ENG-9"))
			if !strings.Contains(msg.note, "changed meanwhile") {
				t.Fatalf("%+v", msg)
			}
			if got := strings.Join(r.calls, " "); got != "fresh worktree:b-ENG-9 fresh" {
				t.Fatalf("updated Linear or prompted anyway: %s", got)
			}
		})
	}
}

// Round 2: a partial result is shown in the popup, not only in a toast that
// may never appear; a clean result closes the popup.
func TestPartialStartStaysOnScreen(t *testing.T) {
	m := withIDs(twoGroups())
	next, cmd := m.Update(actionDoneMsg{note: "The worktree is ready, but Linear wasn't updated (forbidden), so no prompt was sent."})
	m = next.(model)
	if cmd != nil || !strings.Contains(screenText(m), "Linear wasn't updated") {
		t.Fatalf("closed, or didn't say so:\n%s", screenText(m))
	}
	if _, cmd := m.Update(actionDoneMsg{}); cmd == nil {
		t.Fatal("a clean result should close the popup")
	}
}

// Round 2b: the branch or team moving during worktree creation must not send
// the prompt into the worktree made for the old one.
func TestBranchOrTeamChangeDuringWorktreeCreationStops(t *testing.T) {
	for name, change := range map[string]func(*issue){
		"branch": func(f *issue) { f.BranchName = "moved-branch" },
		"team":   func(f *issue) { f.Team = team{ID: "t2", Key: "WEB"} },
	} {
		t.Run(name, func(t *testing.T) {
			r := rig(t)
			later := todo("ENG-11")
			change(&later)
			reads := []issue{todo("ENG-11"), later}
			msg := startWith(r, fakeLinearSource{reads: &reads}, todo("ENG-11"))
			if !strings.Contains(msg.note, "branch or team changed") {
				t.Fatalf("%+v", msg)
			}
			if got := strings.Join(r.calls, " "); got != "fresh worktree:b-ENG-11 fresh" {
				t.Fatalf("updated Linear or prompted anyway: %s", got)
			}
		})
	}
}

// The worktree follows Linear's current branch name, not the loaded one.
func TestWorktreeUsesTheFreshBranch(t *testing.T) {
	r := rig(t)
	fresh := todo("ENG-10")
	fresh.BranchName = "renamed-branch"
	startWith(r, fakeLinearSource{fresh: fresh}, todo("ENG-10"))
	if len(r.calls) < 2 || r.calls[1] != "worktree:renamed-branch" {
		t.Fatalf("%v", r.calls)
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
