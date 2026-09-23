package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// locate finds text on screen as the terminal would show it: line and cell.
func locate(t *testing.T, m model, text string) (x, y int) {
	t.Helper()
	for i, line := range strings.Split(m.View(), "\n") {
		plain := stripANSI(line)
		if j := strings.Index(plain, text); j >= 0 {
			return lipgloss.Width(plain[:j]), i
		}
	}
	t.Fatalf("%q not on screen:\n%s", text, stripANSI(m.View()))
	return 0, 0
}

func click(m model, x, y int) (model, tea.Cmd) {
	next, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	return next.(model), cmd
}

func wheel(m model, down bool) model {
	b := tea.MouseButtonWheelUp
	if down {
		b = tea.MouseButtonWheelDown
	}
	next, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: b})
	return next.(model)
}

func TestFooterSitsOnTheLastLine(t *testing.T) {
	m := withIDs(twoGroups())
	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.height || !strings.Contains(stripANSI(lines[m.height-1]), "enter details") {
		t.Fatalf("list: %d lines, last %q", len(lines), stripANSI(lines[len(lines)-1]))
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	lines = strings.Split(next.(model).View(), "\n")
	if len(lines) != m.height || !strings.Contains(stripANSI(lines[m.height-1]), "c status") {
		t.Fatalf("issue: %d lines, last %q", len(lines), stripANSI(lines[len(lines)-1]))
	}
}

func hover(m model, x, y int) model {
	next, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	return next.(model)
}

func TestOneClickOpens(t *testing.T) {
	m := withIDs(twoGroups())
	x, y := locate(t, m, "A-3")
	m, cmd := click(m, x, y)
	if m.screen != screenIssue || m.cur.Identifier != "A-3" || cmd == nil {
		t.Fatalf("one click should open A-3: screen %v", m.screen)
	}
}

func TestHoverHighlightsWithoutOpening(t *testing.T) {
	m := withIDs(twoGroups())
	x, y := locate(t, m, "A-3")
	m = hover(m, x, y)
	if r := m.selected(); r == nil || r.issue.Identifier != "A-3" || m.screen != screenList {
		t.Fatalf("hover should highlight A-3 only: %+v screen %v", r, m.screen)
	}
	_, hy := locate(t, m, "Todo")
	m = hover(m, x, hy)
	if r := m.selected(); r == nil || r.issue.Identifier != "A-3" {
		t.Fatal("hovering a heading must not move the highlight")
	}
}

func TestClickOnHeadingOrBlankDoesNothing(t *testing.T) {
	m := withIDs(twoGroups())
	_, y := locate(t, m, "Todo")
	m, _ = click(m, 3, y)
	if r := m.selected(); r == nil || r.issue.Identifier != "A-1" {
		t.Fatalf("heading click moved the cursor: %+v", r)
	}
	m, _ = click(m, 3, m.height-3) // empty list area
	if r := m.selected(); r == nil || r.issue.Identifier != "A-1" || m.screen != screenList {
		t.Fatalf("blank click did something: %+v", r)
	}
}

func TestWheelMovesTheCursor(t *testing.T) {
	m := wheel(withIDs(twoGroups()), true)
	if r := m.selected(); r == nil || r.issue.Identifier != "A-2" {
		t.Fatalf("%+v", r)
	}
	m = wheel(m, false)
	if r := m.selected(); r == nil || r.issue.Identifier != "A-1" {
		t.Fatalf("%+v", r)
	}
}

func TestFooterHintsAreButtons(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m.states["team"] = []workflowState{{ID: "t", Name: "Todo", Type: "unstarted"}, {ID: "d", Name: "Done", Type: "completed"}}
	x, y := locate(t, m, "c status")
	m, _ = click(m, x+2, y)
	if m.screen != screenStatus {
		t.Fatalf("clicking 'c status' should open the picker, screen %v", m.screen)
	}
	x, y = locate(t, m, "esc cancel")
	m, _ = click(m, x, y)
	if m.screen != screenIssue {
		t.Fatalf("clicking 'esc cancel' should close the picker, screen %v", m.screen)
	}
	// The separator between hints is not a button.
	x, y = locate(t, m, "·")
	if m2, cmd := click(m, x, y); m2.screen != screenIssue || cmd != nil {
		t.Fatal("separator click did something")
	}
}

func TestClickAStatusToMoveThere(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m.cur.State.ID = "t"
	m.states["team"] = []workflowState{{ID: "t", Name: "Todo", Type: "unstarted"}, {ID: "d", Name: "Done", Type: "completed"}}
	m = key(m, "c")
	x, y := locate(t, m, "Done")
	if hm := hover(m, x, y); hm.stateCursor != 1 || hm.screen != screenStatus {
		t.Fatalf("hover highlights: cursor %d screen %v", hm.stateCursor, hm.screen)
	}
	m, cmd := click(m, x, y)
	if m.mode != modeBusy || cmd == nil || m.stateCursor != 1 {
		t.Fatalf("one click applies: mode %v", m.mode)
	}
}

func TestClickTabsSwitchAndLeaveDetail(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	x, y := locate(t, m, "Projects")
	m, cmd := click(m, x, y)
	if m.screen != screenList || m.tab != tabProjects || cmd == nil {
		t.Fatalf("screen %v tab %v", m.screen, m.tab)
	}
	x, y = locate(t, m, "My issues")
	m, _ = click(m, x, y)
	if m.tab != tabMine {
		t.Fatalf("tab %v", m.tab)
	}
}

func TestClicksIgnoredWhileBusy(t *testing.T) {
	m := withIDs(twoGroups())
	m.mode = modeBusy
	x, y := locate(t, m, "A-3")
	if m, cmd := click(m, x, y); cmd != nil || m.selected().issue.Identifier != "A-1" {
		t.Fatal("a click while busy changed something")
	}
}
