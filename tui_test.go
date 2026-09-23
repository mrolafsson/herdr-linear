package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func listModel(issues ...issue) model {
	m := newModel(context.Background(), config{}, "/repo")
	m.width, m.height = 100, 20
	next, _ := m.Update(issuesMsg{issues: issues})
	return next.(model)
}

func key(m model, k string) model {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, _ := m.Update(msg)
	return next.(model)
}

func twoGroups() model {
	return listModel(
		mkIssue("A-1", "started", "In Progress", 2, 0),
		mkIssue("A-2", "started", "In Progress", 2, 0),
		mkIssue("A-3", "unstarted", "Todo", 1, 0),
	)
}

func TestCursorStartsOnFirstIssueNotHeading(t *testing.T) {
	m := twoGroups()
	if r := m.selected(); r == nil || r.issue.Identifier != "A-1" {
		t.Fatalf("selected %+v", r)
	}
}

func TestCursorSkipsHeadingsBothWays(t *testing.T) {
	m := key(key(twoGroups(), "down"), "down") // A-2 → over "Todo" → A-3
	if r := m.selected(); r == nil || r.issue.Identifier != "A-3" {
		t.Fatalf("down: %+v", r)
	}
	m = key(m, "up")
	if r := m.selected(); r == nil || r.issue.Identifier != "A-2" {
		t.Fatalf("up: %+v", r)
	}
	m = key(key(m, "up"), "up") // can't land on the top heading
	if r := m.selected(); r == nil || r.issue.Identifier != "A-1" {
		t.Fatalf("top: %+v", r)
	}
}

func TestTypingFiltersAndEscClearsThenCloses(t *testing.T) {
	m := twoGroups()
	for _, r := range "a-3" {
		m = key(m, string(r))
	}
	rows := m.rows()
	if len(rows) != 2 || rows[1].issue.Identifier != "A-3" {
		t.Fatalf("filtered rows %+v", rows)
	}
	if r := m.selected(); r == nil || r.issue.Identifier != "A-3" {
		t.Fatalf("selection after filter %+v", r)
	}
	m = key(m, "esc")
	if m.filter.Value() != "" || len(m.rows()) != 6 {
		t.Fatalf("esc should clear the filter first")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc with nothing to clear should quit")
	}
}

func TestArrowsSwitchTabsWhenFilterIsEmpty(t *testing.T) {
	m := twoGroups()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.tab != tabProjects {
		t.Fatal("→ should go to Projects")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if next.(model).tab != tabProjects {
		t.Fatal("→ on the last tab stays put")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if next.(model).tab != tabMine {
		t.Fatal("← should go back to My issues")
	}
}

func TestArrowsEditTheFilterWhenItHasText(t *testing.T) {
	m := twoGroups()
	for _, r := range "ab" {
		m = key(m, string(r))
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = next.(model)
	m = key(m, "X")
	if m.tab != tabMine || m.filter.Value() != "aXb" {
		t.Fatalf("tab %v filter %q: ← should move the text cursor", m.tab, m.filter.Value())
	}
}

func TestLeftLeavesAProjectsIssues(t *testing.T) {
	m := twoGroups()
	m.tab = tabProjects
	m.drilled = &project{ID: "p1", Name: "Daily brief"}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = next.(model)
	if m.drilled != nil || m.tab != tabProjects {
		t.Fatalf("drilled %+v tab %v: ← should return to the projects list", m.drilled, m.tab)
	}
}

func TestBlankLineAboveEveryGroupButTheFirst(t *testing.T) {
	var shape []string
	for _, r := range twoGroups().rows() {
		switch {
		case r.spacer:
			shape = append(shape, "_")
		case r.header != "":
			shape = append(shape, "#"+r.header)
		default:
			shape = append(shape, r.issue.Identifier)
		}
	}
	if got := strings.Join(shape, " "); got != "#In Progress A-1 A-2 _ #Todo A-3" {
		t.Fatalf("rows: %s", got)
	}
}

func TestSignedOutShowsSignIn(t *testing.T) {
	m := newModel(context.Background(), config{}, "")
	m.width, m.height = 80, 20
	next, _ := m.Update(issuesMsg{err: errSignedOut})
	m = next.(model)
	if m.mode != modeSignedOut || !strings.Contains(screenText(m), "sign in") {
		t.Fatalf("mode %v view %q", m.mode, m.View())
	}
}

func TestLoadErrorKeepsListAndShowsError(t *testing.T) {
	m := twoGroups()
	next, _ := m.Update(issuesMsg{err: errors.New("Linear: HTTP 502")})
	m = next.(model)
	if m.mode != modeList || !strings.Contains(screenText(m), "HTTP 502") {
		t.Fatalf("mode %v", m.mode)
	}
}

func TestWorktreeMarker(t *testing.T) {
	is := mkIssue("A-1", "started", "In Progress", 0, 0)
	is.BranchName = "me/a-1"
	m := listModel(is)
	next, _ := m.Update(worktreesMsg{"me/a-1": true})
	if !strings.Contains(next.(model).View(), "⎇") {
		t.Fatal("no worktree marker")
	}
}

func TestRowsFitTheWidth(t *testing.T) {
	is := mkIssue("A-1", "started", "In Progress", 0, 0)
	is.Title = strings.Repeat("very long title ", 20)
	m := listModel(is)
	for _, line := range strings.Split(m.View(), "\n") {
		if w := len([]rune(stripANSI(line))); w > m.width {
			t.Fatalf("line %d cells wide > %d: %q", w, m.width, line)
		}
	}
}

func stripANSI(s string) string { return stripStyles(s) }

// screenText is the screen as the user reads it: styling is often applied per
// word, so assertions on raw View() output can pass or fail for the wrong reason.
func screenText(m model) string { return stripStyles(m.View()) }
