package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// typeText types into the focused field. The cursor's blink is all a
// keystroke there returns, so it isn't drained: that would wait on it.
func typeText(m model, s string) model {
	for _, r := range s {
		next, _ := m.Update(keyMsg(string(r)))
		m = next.(model)
	}
	return m
}

func TestQuickAddFromTheList(t *testing.T) {
	m := press(demoModel(t), "ctrl+c")
	if m.screen != screenCreate || m.form.teamID != demoTeamID || m.form.projectID != "" {
		t.Fatalf("screen %v team %q project %q", m.screen, m.form.teamID, m.form.projectID)
	}
	if s := screenText(m); !strings.Contains(s, "New issue") || !strings.Contains(s, "Todo") || !strings.Contains(s, "You") {
		t.Fatalf("form:\n%s", s)
	}
	before := len(m.issues)
	m = press(typeText(m, "Undo for deleted notes"), "enter")
	if m.screen != screenCreated || m.created == nil || m.created.Identifier != "HAL-253" {
		t.Fatalf("screen %v created %+v err %q", m.screen, m.created, m.err)
	}
	if m.created.State.Name != "Todo" || m.created.Assignee == nil || !m.created.Assignee.IsMe {
		t.Fatalf("created %+v", m.created)
	}
	if len(m.issues) != before+1 {
		t.Fatal("the new issue isn't in your list")
	}
	m = press(m, "enter")
	if m.screen != screenIssue || m.cur.Identifier != "HAL-253" {
		t.Fatalf("view: screen %v", m.screen)
	}
	m = press(m, "s")
	if !strings.Contains(m.flash, "/ticket HAL-253") {
		t.Fatalf("start: %q %q", m.flash, m.err)
	}
}

func TestQuickAddNeedsATitle(t *testing.T) {
	m := press(press(demoModel(t), "ctrl+c"), "enter")
	if m.screen != screenCreate || m.err == "" {
		t.Fatalf("screen %v err %q", m.screen, m.err)
	}
}

func TestQuickAddFromAProjectFillsItIn(t *testing.T) {
	m := press(press(demoModel(t), "tab"), "enter") // Offline sync's screen
	m = press(m, "c")
	if m.screen != screenCreate || m.form.projectID != "a1f3" || m.form.teamID != demoTeamID {
		t.Fatalf("screen %v project %q team %q", m.screen, m.form.projectID, m.form.teamID)
	}
	m = press(typeText(m, "Retry uploads"), "enter")
	if m.created == nil || m.created.Project == nil || m.created.Project.Name != "Offline sync" {
		t.Fatalf("created %+v", m.created)
	}
	// Another keeps the project; esc goes back to the project screen.
	m = press(m, "c")
	if m.screen != screenCreate || m.form.projectID != "a1f3" || m.form.title.Value() != "" {
		t.Fatalf("another: screen %v project %q title %q", m.screen, m.form.projectID, m.form.title.Value())
	}
	if m = press(m, "esc"); m.screen != screenProject {
		t.Fatalf("esc: screen %v", m.screen)
	}
}

func TestQuickAddOnASelectedProjectAndInADrilledOne(t *testing.T) {
	m := press(press(demoModel(t), "tab"), "down") // Search 2.0, selected
	if f := press(m, "ctrl+c").form; f.projectID != "b27c" {
		t.Fatalf("selected project: %q", f.projectID)
	}
	m = press(press(press(demoModel(t), "tab"), "enter"), "i") // in Offline sync's issues
	n := len(m.projIss)
	m = press(typeText(press(m, "ctrl+c"), "Retry uploads"), "enter")
	if len(m.projIss) != n+1 {
		t.Fatal("the new issue isn't listed in its project")
	}
}

func TestQuickAddChoosesFromLists(t *testing.T) {
	m := press(demoModel(t), "ctrl+c")
	m = typeText(m, "Faster search")
	m = press(press(press(m, "tab"), "tab"), "tab") // → Project
	if m.form.focus != fieldProject {
		t.Fatalf("focus %d", m.form.focus)
	}
	m = typeText(m, "search") // typing opens the list, filtered
	if !m.form.picking || len(m.picked()) != 1 {
		t.Fatalf("picking %v %+v", m.form.picking, m.picked())
	}
	m = press(m, "enter")
	if m.form.picking || m.form.projectID != "b27c" {
		t.Fatalf("project %q", m.form.projectID)
	}
	m = press(press(m, "down"), "right") // Status: Todo → In Progress
	m = press(press(m, "down"), "right") // Priority: none → Urgent
	m = press(press(m, "down"), "left")  // Assignee: you → nobody
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = drain(next.(model), cmd)
	c := m.created
	if c == nil || c.State.Name != "In Progress" || c.Priority != 1 || c.Assignee != nil || c.Project.ID != "b27c" {
		t.Fatalf("created %+v (err %q)", c, m.err)
	}
	if strings.Contains(screenText(m), "panic") {
		t.Fatal(screenText(m))
	}
}

func TestQuickAddDetailsTakeNewlines(t *testing.T) {
	m := press(press(demoModel(t), "ctrl+c"), "tab")
	m = press(typeText(m, "one"), "enter")
	m = typeText(m, "two")
	if m.screen != screenCreate || m.form.desc.Value() != "one\ntwo" {
		t.Fatalf("screen %v desc %q", m.screen, m.form.desc.Value())
	}
}

func TestCtrlCCreatesOnlyInTheList(t *testing.T) {
	m := demoModel(t)
	if next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); next.(model).screen != screenCreate || cmd != nil && isQuit(cmd) {
		t.Fatal("ctrl+c in the list should open the form, not close")
	}
	m = press(m, "enter") // an issue screen: ctrl+c closes
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil || !isQuit(cmd) {
		t.Fatal("ctrl+c on an issue screen should close")
	}
}

func isQuit(cmd tea.Cmd) bool {
	_, ok := cmd().(tea.QuitMsg)
	return ok
}
