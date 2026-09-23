package main

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// demoSource is a fictional Linear workspace ("Halcyon", a notes app) held in
// memory: `picker --demo`. It exists for screenshots and for trying the plugin
// without an account, so it never touches the network, the keychain or git.
// Changes (status, start) stick for the life of the popup.
type demoSource struct {
	mu          sync.Mutex
	issues      []issue
	projs       []project
	states      map[string][]workflowState // by team id
	details     map[string]*issueDetail
	projDetails map[string]*projectDetail
	trees       map[string]bool // branches with a worktree
}

// demoDoneMsg reports a worktree action the demo only pretended to do.
type demoDoneMsg struct {
	branch  string
	note    string
	issueID string
	state   *workflowState // set when start moved the issue
}

var (
	demoMe     = &person{ID: "u-me", Name: "Sam Rivera", IsMe: true}
	demoPriya  = &person{ID: "u-priya", Name: "Priya Natarajan"}
	demoTomas  = &person{ID: "u-tomas", Name: "Tomás Eriksen"}
	demoAda    = &person{ID: "u-ada", Name: "Ada Okafor"}
	demoTeamID = "t-hal"
)

func demoStates() []workflowState {
	return []workflowState{
		{ID: "s-triage", Name: "Triage", Type: "triage", Color: "#fc7840", Position: 0},
		{ID: "s-backlog", Name: "Backlog", Type: "backlog", Color: "#bec2c8", Position: 1},
		{ID: "s-todo", Name: "Todo", Type: "unstarted", Color: "#e2e2e2", Position: 2},
		{ID: "s-progress", Name: "In Progress", Type: "started", Color: "#f2c94c", Position: 3},
		{ID: "s-review", Name: "In Review", Type: "started", Color: "#26b5ce", Position: 4},
		{ID: "s-done", Name: "Done", Type: "completed", Color: "#5e6ad2", Position: 5},
		{ID: "s-canceled", Name: "Canceled", Type: "canceled", Color: "#95a2b3", Position: 6},
	}
}

func newDemoSource() *demoSource {
	d := &demoSource{
		states:      map[string][]workflowState{demoTeamID: demoStates()},
		details:     map[string]*issueDetail{},
		projDetails: map[string]*projectDetail{},
		trees:       map[string]bool{},
	}
	state := func(id string) workflowState {
		for _, s := range demoStates() {
			if s.ID == id {
				return s
			}
		}
		panic("demo: no state " + id)
	}

	proj := func(id, name, statusName, statusType, color string, progress float64, target string, lead *person) {
		p := project{
			ID: id, Name: name, SlugID: id, Color: color, Progress: progress, TargetDate: target, Lead: lead,
			URL:    "https://linear.app/halcyon/project/" + slugify(name) + "-" + id,
			Status: projectStatus{Name: statusName, Type: statusType, Color: color},
		}
		p.Teams.Nodes = []teamKey{{"HAL"}}
		d.projs = append(d.projs, p)
	}
	proj("a1f3", "Offline sync", "In Progress", "started", "#f2c94c", 0.64, "2026-10-15", demoMe)
	proj("b27c", "Search 2.0", "In Progress", "started", "#f2c94c", 0.38, "2026-11-01", demoPriya)
	proj("c9d0", "Export everything", "Planned", "planned", "#e2e2e2", 0.1, "2026-12-01", demoMe)
	proj("d4e8", "Platform hardening", "Paused", "paused", "#95a2b3", 0.22, "", demoTomas)
	proj("e5a2", "Home screen widgets", "Backlog", "backlog", "#bec2c8", 0, "", demoAda)

	ref := func(id string) *projectRef {
		for _, p := range d.projs {
			if p.ID == id {
				return &projectRef{ID: p.ID, Name: p.Name}
			}
		}
		return nil
	}
	add := func(num, title, stateID string, prio int, projectID string, who *person, tree bool) {
		id := "HAL-" + num
		is := issue{
			ID: "i-" + num, Identifier: id, Title: title, Priority: prio,
			State: state(stateID), Team: team{ID: demoTeamID, Key: "HAL"},
			BranchName: "sam/hal-" + num + "-" + slugify(title),
			URL:        "https://linear.app/halcyon/issue/" + id,
			Assignee:   who,
		}
		if projectID != "" {
			is.Project = ref(projectID)
		}
		d.issues = append(d.issues, is)
		if tree {
			d.trees[is.BranchName] = true
		}
	}
	// Assigned to you.
	add("212", "Resolve conflicting offline edits with a three-way merge", "s-review", 2, "a1f3", demoMe, true)
	add("231", "Search index drops accents in note titles", "s-progress", 1, "b27c", demoMe, true)
	add("198", "Share sheet remembers the last chosen folder", "s-progress", 3, "", demoMe, false)
	add("240", "Markdown export keeps inline images", "s-todo", 2, "c9d0", demoMe, false)
	add("244", "Keyboard shortcut cheat sheet (⌘/)", "s-todo", 4, "", demoMe, false)
	add("247", "Rate-limit the public share endpoint", "s-todo", 2, "d4e8", demoMe, false)
	add("252", "Crash when pasting a 40 MB table", "s-triage", 1, "", demoMe, false)
	add("150", "Themeable accent colours", "s-backlog", 0, "", demoMe, false)
	add("163", "Import notes from Bear", "s-backlog", 3, "c9d0", demoMe, false)
	add("171", "Nested tags in the sidebar", "s-backlog", 0, "", demoMe, false)
	// Teammates' work, seen inside projects.
	add("205", "Sync queue survives an app kill mid-upload", "s-progress", 2, "a1f3", demoTomas, false)
	add("219", "Conflict banner copy and undo", "s-todo", 3, "a1f3", demoAda, false)
	add("226", "Tombstones for notes deleted on another device", "s-backlog", 3, "a1f3", nil, false)
	add("233", "Typo-tolerant matching (edit distance 1)", "s-review", 2, "b27c", demoPriya, false)
	add("238", "Rank recently opened notes higher", "s-todo", 3, "b27c", nil, false)
	add("249", "PDF export with a table of contents", "s-backlog", 4, "c9d0", demoAda, false)

	d.details["i-231"] = &issueDetail{
		PriorityLabel: "Urgent", DueDate: "2026-09-26", Cycle: &cycle{Number: 14, Name: "Search polish"},
		Labels: struct {
			Nodes []label `json:"nodes"`
		}{Nodes: []label{{"bug", "#eb5757"}, {"search", "#4ea7fc"}}},
		Description: "Searching for `cafe` doesn't find a note titled **Café notes**. " +
			"Bodies are folded to ASCII, titles aren't.\n\n" +
			"## Repro\n\n1. Create a note titled *Crème brûlée*\n2. Search for `creme`\n3. Nothing comes back\n\n" +
			"## Fix\n\nFold titles the same way we fold bodies, at index time:\n\n" +
			"```go\nfunc fold(s string) string {\n\treturn norm.NFKD.String(strings.ToLower(s))\n}\n```\n\n" +
			"- [x] Failing test\n- [ ] Reindex existing notes on upgrade\n- [ ] Check CJK titles still match\n\n" +
			"> Priya: the reindex can run in the background; titles are small.",
	}
	d.details["i-212"] = &issueDetail{
		PriorityLabel: "High", Cycle: &cycle{Number: 14},
		Labels: struct {
			Nodes []label `json:"nodes"`
		}{Nodes: []label{{"sync", "#26b5ce"}}},
		Description: "When two devices edit the same note offline, the last write wins and the other edit is lost.\n\n" +
			"Keep the common ancestor and merge paragraph by paragraph. When both sides change the same paragraph, " +
			"keep both and show the **conflict banner** (HAL-219).\n\n" +
			"| Case | Result |\n|---|---|\n| Different paragraphs | Merged silently |\n| Same paragraph | Both kept, banner shown |\n| Delete vs edit | Edit wins |",
	}
	d.projDetails["a1f3"] = &projectDetail{
		Lead: demoMe, StartDate: "2026-08-04",
		Content: "## Why\n\nPeople write on planes and in basements. Today an edit made offline can be lost when two devices disagree.\n\n" +
			"## Scope\n\n- Queue writes locally and replay them in order\n- Three-way merge for conflicting edits\n- A banner, not a modal, when we couldn't merge\n\n" +
			"## Not in scope\n\nReal-time collaboration. That's a different project.",
	}
	return d
}

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func isOpenState(t string) bool { return t != "completed" && t != "canceled" }

func (d *demoSource) myIssues(context.Context) ([]issue, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []issue
	for _, is := range d.issues {
		if is.Assignee != nil && is.Assignee.IsMe && isOpenState(is.State.Type) {
			out = append(out, is)
		}
	}
	sortIssues(out)
	return out, nil
}

func (d *demoSource) projects(context.Context) ([]project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ps := append([]project(nil), d.projs...)
	sortProjects(ps)
	return ps, nil
}

func (d *demoSource) projectIssues(_ context.Context, projectID string) ([]issue, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []issue
	for _, is := range d.issues {
		if is.Project != nil && is.Project.ID == projectID && isOpenState(is.State.Type) {
			out = append(out, is)
		}
	}
	sortIssues(out)
	return out, nil
}

func (d *demoSource) issueDetail(_ context.Context, id string) (*issueDetail, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if det, ok := d.details[id]; ok {
		return det, nil
	}
	det := &issueDetail{Description: "A demo issue. The real picker shows its Linear description here, rendered as Markdown."}
	for _, is := range d.issues {
		if is.ID == id {
			det.PriorityLabel = []string{"No priority", "Urgent", "High", "Medium", "Low"}[is.Priority]
		}
	}
	return det, nil
}

func (d *demoSource) projectDetail(_ context.Context, id string) (*projectDetail, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if det, ok := d.projDetails[id]; ok {
		return det, nil
	}
	for _, p := range d.projs {
		if p.ID == id {
			return &projectDetail{Lead: p.Lead, Description: "A demo project in the fictional Halcyon workspace."}, nil
		}
	}
	return nil, errors.New("no such project")
}

func (d *demoSource) teamStates(_ context.Context, teamID string) ([]workflowState, error) {
	return d.states[teamID], nil
}

func (d *demoSource) setState(_ context.Context, issueID, stateID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.setStateLocked(issueID, stateID)
}

func (d *demoSource) setStateLocked(issueID, stateID string) error {
	for i := range d.issues {
		if d.issues[i].ID != issueID {
			continue
		}
		for _, s := range d.states[d.issues[i].Team.ID] {
			if s.ID == stateID {
				d.issues[i].State = s
				return nil
			}
		}
		return errors.New("no such state")
	}
	return errors.New("no such issue")
}

func (d *demoSource) freshIssue(_ context.Context, id string) (issue, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, is := range d.issues {
		if is.ID == id {
			return is, nil
		}
	}
	return issue{}, errors.New("no such issue")
}

func (d *demoSource) startIssue(_ context.Context, is issue) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.startLocked(is)
}

func (d *demoSource) startLocked(is issue) error {
	for i := range d.issues {
		if d.issues[i].ID != is.ID {
			continue
		}
		if d.issues[i].State.Type != "started" {
			if err := d.setStateLocked(is.ID, "s-progress"); err != nil {
				return err
			}
		}
		if d.issues[i].Assignee == nil {
			d.issues[i].Assignee = demoMe
		}
		return nil
	}
	return errors.New("no such issue")
}

func (d *demoSource) worktreeBranches() map[string]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]bool{}
	for b := range d.trees {
		out[b] = true
	}
	return out
}

// worktree stands in for the herdr side: nothing is created, the popup stays
// open, and the footer says what the real plugin would have done.
func (d *demoSource) worktree(t target, is *issue, start bool) demoDoneMsg {
	d.mu.Lock()
	defer d.mu.Unlock()
	msg := demoDoneMsg{branch: t.Branch}
	verb := "open the worktree"
	if !d.trees[t.Branch] {
		verb = "create a worktree"
	}
	d.trees[t.Branch] = true
	if start && is != nil {
		if err := d.startLocked(*is); err == nil {
			for _, x := range d.issues {
				if x.ID == is.ID {
					s := x.State
					msg.issueID, msg.state = is.ID, &s
				}
			}
		}
		msg.note = "Demo: would move " + is.Identifier + " to In Progress, " + verb + " and send /ticket " + is.Identifier
		return msg
	}
	msg.note = "Demo: would " + verb + " on " + t.Branch
	return msg
}
