package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	next, _ := m.Update(worktreesMsg{branches: map[string]bool{"me/a-1": true}})
	if !strings.Contains(next.(model).View(), "⌥") {
		t.Fatal("no worktree marker")
	}
}

// Measured in terminal cells, the unit the popup is sized in: a CJK
// character or an emoji takes two, so counting characters would pass while
// the row overflows.
func TestRowsFitTheWidth(t *testing.T) {
	for name, title := range map[string]string{
		"ascii": strings.Repeat("very long title ", 20),
		"cjk":   strings.Repeat("日本語のタイトル", 20),
		"emoji": strings.Repeat("🚀 ship it ", 20),
	} {
		t.Run(name, func(t *testing.T) {
			is := mkIssue("A-1", "started", "In Progress", 0, 0)
			is.Title = title
			is.Project = &projectRef{Name: "プロジェクト"}
			m := listModel(is)
			for _, line := range strings.Split(m.View(), "\n") {
				if w := lipgloss.Width(line); w > m.width {
					t.Fatalf("line %d cells wide > %d: %q", w, m.width, stripANSI(line))
				}
			}
		})
	}
}

func stripANSI(s string) string { return stripStyles(s) }

// screenText is the screen as the user reads it: styling is often applied per
// word, so assertions on raw View() output can pass or fail for the wrong reason.
func screenText(m model) string { return stripStyles(m.View()) }

func TestProjectsAreGroupedByStatusYoursFirst(t *testing.T) {
	st := func(name, typ string) projectStatus { return projectStatus{Name: name, Type: typ} }
	me := &person{IsMe: true}
	ps := []project{
		{Name: "backlog one", Status: st("Backlog", "backlog")},
		{Name: "theirs", Status: st("In Progress", "started")},
		{Name: "planned", Status: st("Planned", "planned")},
		{Name: "mine", Status: st("In Progress", "started"), Lead: me},
	}
	sortProjects(ps)
	m := newModel(context.Background(), config{}, "")
	m.tab, m.projects = tabProjects, ps
	var shape []string
	for _, r := range m.rows() {
		switch {
		case r.spacer:
			shape = append(shape, "_")
		case r.header != "":
			shape = append(shape, "#"+r.header)
		default:
			shape = append(shape, r.project.Name)
		}
	}
	want := "#In Progress mine theirs _ #Planned planned _ #Backlog backlog one"
	if got := strings.Join(shape, " "); got != want {
		t.Fatalf("rows:\n got %s\nwant %s", got, want)
	}
}

// typeLookup types q into the filter and runs the lookup it schedules, as if
// typing had paused and Linear had answered.
func typeLookup(m model, q string) model {
	for _, r := range q {
		m = key(m, string(r))
	}
	next, cmd := m.Update(lookupTickMsg{m.lookingUp, m.gen})
	m = next.(model)
	if cmd != nil {
		next, _ = m.Update(cmd())
		m = next.(model)
	}
	return m
}

// otherTeams puts a second team's HAL-205 twin in the demo, as OPS-205.
func otherTeams() *demoSource {
	d := newDemoSource()
	ops := d.issues[len(d.issues)-1]
	ops.ID, ops.Identifier, ops.Team = "i-ops-205", "OPS-205", team{ID: "t-ops", Key: "OPS"}
	for _, is := range d.issues {
		if is.Identifier == "HAL-205" {
			ops.Title, ops.State = "Ops twin", is.State
		}
	}
	d.issues = append(d.issues, ops)
	return d
}

func TestFilterByNumberFindsEveryTeamsIssue(t *testing.T) {
	m := listModel(mkIssue("HAL-212", "started", "In Progress", 3, 0))
	m.client = otherTeams()
	m = typeLookup(m, "205")
	var got []string
	for _, r := range m.rows() {
		if r.issue != nil {
			got = append(got, r.issue.Identifier)
		}
	}
	if strings.Join(got, " ") != "HAL-205 OPS-205" {
		t.Fatalf("rows %v", got)
	}
	if !strings.Contains(m.View(), "Not in your issues") {
		t.Fatalf("no heading:\n%s", m.View())
	}
	// Editing the filter away from it drops them.
	m = key(m, "9")
	if r := m.selected(); r != nil {
		t.Fatalf("still listed: %+v", r)
	}
}

func TestFilterByIdentifierFindsThatTeamsOnly(t *testing.T) {
	m := listModel()
	m.client = otherTeams()
	m = typeLookup(m, "ops-205")
	if rows := m.rows(); len(rows) != 2 || rows[1].issue.Identifier != "OPS-205" {
		t.Fatalf("rows %+v", rows)
	}
}

func TestYourOwnIssueIsListedOnce(t *testing.T) {
	mine := mkIssue("HAL-212", "started", "In Progress", 3, 0)
	mine.ID = "i-212"
	m := listModel(mine)
	m.client = newDemoSource()
	m = typeLookup(m, "212") // found by Linear too, but it's already listed
	if rows := m.rows(); len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	m = key(m, "esc")
	for _, r := range "hal-212" {
		m = key(m, string(r))
	}
	if m.lookingUp != "" {
		t.Fatalf("looked up your own issue")
	}
}

func TestNoLookupForWords(t *testing.T) {
	m := listModel()
	m.client = newDemoSource()
	for _, r := range "search" {
		m = key(m, string(r))
	}
	if m.lookingUp != "" {
		t.Fatalf("looked up %q", m.lookingUp)
	}
	m = key(m, "esc")
	if m = typeLookup(m, "9999"); len(m.rows()) != 0 || m.lookingUp != "" {
		t.Fatalf("rows %+v, lookingUp %q", m.rows(), m.lookingUp)
	}
}

func TestStaleLookupIsDropped(t *testing.T) {
	m := listModel()
	m.client = newDemoSource()
	for _, r := range "20" {
		m = key(m, string(r))
	}
	next, cmd := m.Update(lookupTickMsg{m.lookingUp, m.gen})
	m = next.(model)
	m = key(m, "5") // typed on while Linear was answering
	next, _ = m.Update(cmd())
	if m = next.(model); m.lookup != nil {
		t.Fatalf("stale answer shown: %+v", m.lookup)
	}
}

// signingIn is the popup mid sign-in, with no sign-in behind it.
func signingIn() model {
	m := newModel(context.Background(), config{}, "")
	m.width, m.height, m.mode = 60, 30, modeSigningIn
	m.paste = textinput.New()
	m.paste.Focus()
	m.pasted = make(chan string, 1)
	return m
}

func TestSignInKeepsTheLinkOnScreen(t *testing.T) {
	m := signingIn()
	long := authorizeURL + "?" + strings.Repeat("x", 200) + "END"
	next, _ := m.Update(loginLinkMsg{long, false})
	next, _ = next.(model).Update(loginStatusMsg("Waiting for you to approve access in the browser…"))
	m = next.(model)
	view := ansi.Strip(m.View())
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Errorf("line wider than the popup: %q", line)
		}
	}
	joined := strings.Join(strings.Fields(view), "")
	if !strings.Contains(joined, strings.Join(strings.Fields(long), "")) {
		t.Errorf("the whole link isn't on screen after the next status line:\n%s", view)
	}
	if !strings.Contains(view, "on any computer") || !strings.Contains(view, "Waiting") {
		t.Errorf("view:\n%s", view)
	}
}

func TestSignInPasteGoesToTheSignIn(t *testing.T) {
	m := signingIn()
	next, _ := m.Update(loginLinkMsg{authorizeURL, true})
	m = next.(model)
	m = key(m, "enter") // nothing typed: nothing sent
	select {
	case p := <-m.pasted:
		t.Fatalf("sent %q", p)
	default:
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(redirectURI + "?code=c&state=s"), Paste: true})
	m = key(next.(model), "enter")
	select {
	case p := <-m.pasted:
		if p != redirectURI+"?code=c&state=s" {
			t.Errorf("sent %q", p)
		}
	default:
		t.Fatal("nothing sent")
	}
	if m.paste.Value() != "" {
		t.Errorf("field kept %q", m.paste.Value())
	}
}
