package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func withIDs(m model) model {
	for i := range m.issues {
		m.issues[i].ID = "id-" + m.issues[i].Identifier
		m.issues[i].Team.ID = "team"
	}
	return m
}

func TestEnterOpensIssueScreenAndEscReturns(t *testing.T) {
	m := withIDs(twoGroups())
	m = key(m, "down") // A-2
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.screen != screenIssue || m.cur.Identifier != "A-2" || cmd == nil {
		t.Fatalf("screen %v cur %+v cmd %v", m.screen, m.cur, cmd)
	}
	if !strings.Contains(screenText(m), "A-2") || !strings.Contains(screenText(m), "c status") {
		t.Fatalf("issue view:\n%s", m.View())
	}
	// Letters are commands here, not filter input.
	m = key(m, "x")
	if m.filter.Value() != "" {
		t.Fatal("typing on the issue screen leaked into the filter")
	}
	m = key(m, "esc")
	if m.screen != screenList {
		t.Fatal("esc should go back to the list")
	}
	if r := m.selected(); r == nil || r.issue.Identifier != "A-2" {
		t.Fatalf("list cursor lost: %+v", r)
	}
}

func TestIssueDetailArrivesAndShowsDescription(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	d := &issueDetail{Description: "Make the brief say what moved.", PriorityLabel: "High"}
	next, _ = m.Update(issueDetailMsg{id: "id-A-1", detail: d})
	m = next.(model)
	if !strings.Contains(stripANSI(m.View()), "Make the brief say what moved.") {
		t.Fatalf("no description:\n%s", m.View())
	}
	// A late reply for another issue must not overwrite this one.
	next, _ = m.Update(issueDetailMsg{id: "id-A-3", detail: &issueDetail{Description: "wrong"}})
	if strings.Contains(screenText(next.(model)), "wrong") {
		t.Fatal("stale detail replaced the current one")
	}
}

func TestLongDescriptionIsClampedToThePopup(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(issueDetailMsg{id: "id-A-1", detail: &issueDetail{Description: strings.Repeat("line\n", 200)}})
	m = next.(model)
	v := screenText(m)
	if n := strings.Count(v, "\n") + 1; n > m.height {
		t.Fatalf("view is %d lines, popup is %d", n, m.height)
	}
	if !strings.Contains(v, "more lines") {
		t.Fatal("a cut description should say so")
	}
}

func TestStatusPickerMovesIssueAndResortsList(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // A-1, In Progress
	m = next.(model)
	m.cur.State.ID = "progress"
	m.states["team"] = []workflowState{
		{ID: "todo", Name: "Todo", Type: "unstarted", Position: 1},
		{ID: "progress", Name: "In Progress", Type: "started", Position: 2},
		{ID: "done", Name: "Done", Type: "completed", Position: 3},
	}
	m = key(m, "c")
	if m.screen != screenStatus || m.stateCursor != 1 {
		t.Fatalf("picker should open on the current state: screen %v cursor %d", m.screen, m.stateCursor)
	}
	m = key(m, "down")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeBusy || cmd == nil || m.screen != screenIssue {
		t.Fatalf("mode %v screen %v", m.mode, m.screen)
	}
	done := workflowState{ID: "done", Name: "Done", Type: "completed", Position: 3}
	next, _ = m.Update(stateSetMsg{issueID: "id-A-1", state: done})
	m = next.(model)
	if m.cur.State.ID != "done" || m.flash != "Moved to Done" {
		t.Fatalf("cur %+v flash %q", m.cur.State, m.flash)
	}
	last := m.issues[len(m.issues)-1]
	if last.Identifier != "A-1" || last.State.ID != "done" {
		t.Fatalf("list not updated and re-sorted: last is %+v", last)
	}
}

func TestPickingTheCurrentStateDoesNothing(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m.cur.State.ID = "progress"
	m.states["team"] = []workflowState{{ID: "progress", Name: "In Progress", Type: "started"}}
	m = key(m, "c")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || next.(model).mode == modeBusy {
		t.Fatal("no update for an unchanged state")
	}
}

func TestFailedStateChangeKeepsTheOldState(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	before := m.cur.State
	next, _ = m.Update(stateSetMsg{issueID: "id-A-1", state: workflowState{ID: "done"}, err: errors.New("Linear refused the update")})
	m = next.(model)
	if m.cur.State != before || !strings.Contains(screenText(m), "refused") {
		t.Fatalf("state %+v view:\n%s", m.cur.State, m.View())
	}
}

func TestProjectScreenDrillsIntoIssues(t *testing.T) {
	m := newModel(context.Background(), config{}, "/repo")
	m.width, m.height = 100, 24
	m.tab = tabProjects
	next, _ := m.Update(projectsMsg{projects: []project{{ID: "p1", Name: "Daily brief", URL: "https://linear.app/x/project/daily-brief-ab12"}}})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.screen != screenProject || !strings.Contains(screenText(m), "project/daily-brief-ab12") {
		t.Fatalf("screen %v view:\n%s", m.screen, m.View())
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = next.(model)
	if m.screen != screenList || m.drilled == nil || m.drilled.ID != "p1" || cmd == nil {
		t.Fatalf("screen %v drilled %+v", m.screen, m.drilled)
	}
}

func TestTeamStatesInLifecycleOrder(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)})
	fakeLinear(t, reply(200, map[string]any{"data": map[string]any{"team": map[string]any{"states": map[string]any{"nodes": []any{
		map[string]any{"id": "done", "type": "completed", "position": 0},
		map[string]any{"id": "review", "type": "started", "position": 3},
		map[string]any{"id": "backlog", "type": "backlog", "position": 9},
		map[string]any{"id": "progress", "type": "started", "position": 2},
		map[string]any{"id": "todo", "type": "unstarted", "position": 1},
	}}}}}))
	states, err := (&linearClient{}).teamStates(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range states {
		ids = append(ids, s.ID)
	}
	if got := strings.Join(ids, " "); got != "backlog todo progress review done" {
		t.Fatalf("order %s", got)
	}
}
