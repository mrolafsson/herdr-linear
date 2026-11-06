package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	if len(rows) != 1 || rows[0].issue.Identifier != "A-3" {
		t.Fatalf("filtered rows %+v", rows)
	}
	if r := m.selected(); r == nil || r.issue.Identifier != "A-3" {
		t.Fatalf("selection after filter %+v", r)
	}
	m = key(m, "esc")
	if m.filter.Value() != "" || len(m.rows()) != 3 {
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
	if m = next.(model); m.tab != tabRecent {
		t.Fatal("→ again should go to Recent")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if next.(model).tab != tabRecent {
		t.Fatal("→ on the last tab stays put")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m = next.(model); m.tab != tabProjects {
		t.Fatal("← should go back to Projects")
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
	if got := listShape(twoGroups()); got != "#In Progress 2 A-1 A-2 _ #Todo 1 A-3" {
		t.Fatalf("list: %s", got)
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
	if !strings.Contains(screenText(next.(model)), "worktree") {
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
	m.width, m.height, m.mode, m.loaded[tabProjects] = 80, 30, modeList, true
	want := "#In Progress 2 mine theirs _ #Planned 1 planned _ #Backlog 1 backlog one"
	if got := listShape(m); got != want {
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
	if rows := m.rows(); len(rows) != 1 || rows[0].issue.Identifier != "OPS-205" {
		t.Fatalf("rows %+v", rows)
	}
}

func TestYourOwnIssueIsListedOnce(t *testing.T) {
	mine := mkIssue("HAL-212", "started", "In Progress", 3, 0)
	mine.ID = "i-212"
	m := listModel(mine)
	m.client = newDemoSource()
	m = typeLookup(m, "212") // found by Linear too, but it's already listed
	if rows := m.rows(); len(rows) != 1 {
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

// countingSource counts what the filter asks Linear, and finds what it's
// told: a number by lookup, anything by search.
type countingSource struct {
	*demoSource
	byNumber, searched []string
	numbered, found    []issue
}

func (c *countingSource) issuesByNumber(_ context.Context, number int, team string) ([]issue, error) {
	c.byNumber = append(c.byNumber, fmt.Sprintf("%s-%d", team, number))
	return c.numbered, nil
}

func (c *countingSource) searchIssues(_ context.Context, term string) ([]issue, error) {
	c.searched = append(c.searched, term)
	return c.found, nil
}

func TestFilterAsksLinearOnlyWhatItMust(t *testing.T) {
	mine := mkIssue("HAL-212", "started", "In Progress", 3, 0)
	mine.ID, mine.Title = "i-212", "Fix the 2048 search widget"
	hit := mkIssue("OPS-9", "completed", "Done", 3, 0)
	hit.ID, hit.Title = "i-9", "Mentioned in the description only"
	numbered := mkIssue("OPS-2048", "started", "In Progress", 3, 0)
	numbered.ID = "i-2048"

	for _, c := range []struct {
		name, typed        string
		numbered, found    []issue
		byNumber, searched string
		rows               string
	}{
		{name: "words your list has: nothing asked", typed: "widget", rows: "HAL-212"},
		{name: "words it hasn't: searched, as typed", typed: "Kerning", found: []issue{hit}, searched: "Kerning", rows: "OPS-9"},
		{name: "too short to search", typed: "zz"},
		{name: "a number found by number: not searched", typed: "3000", numbered: []issue{numbered}, byNumber: "-3000", rows: "OPS-2048"},
		{name: "a number found nowhere: then searched", typed: "9999", found: []issue{hit}, byNumber: "-9999", searched: "9999", rows: "OPS-9"},
		{name: "an identifier found nowhere: then searched", typed: "eng-77", found: []issue{hit}, byNumber: "ENG-77", searched: "eng-77", rows: "OPS-9"},
		{name: "a number your list matches: nothing asked", typed: "2048", rows: "HAL-212"},
		{name: "a key your list has: nothing asked", typed: "hal-212", rows: "HAL-212"},
		{name: "found nowhere at all", typed: "kerning", searched: "kerning"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := &countingSource{demoSource: newDemoSource(), numbered: c.numbered, found: c.found}
			m := listModel(mine)
			m.client = src
			m = typeLookup(m, c.typed)
			var rows []string
			for _, r := range m.rows() {
				rows = append(rows, r.issue.Identifier)
			}
			if got := strings.Join(rows, " "); got != c.rows {
				t.Errorf("rows %q, want %q", got, c.rows)
			}
			if got := strings.Join(src.byNumber, " "); got != c.byNumber {
				t.Errorf("by number %q, want %q", got, c.byNumber)
			}
			if got := strings.Join(src.searched, " "); got != c.searched {
				t.Errorf("searched %q, want %q", got, c.searched)
			}
			if m.lookingUp != "" {
				t.Errorf("still looking up %q", m.lookingUp)
			}
		})
	}
}

func TestSpacesInTheFilterDontSearchAgain(t *testing.T) {
	m := listModel()
	m.client = newDemoSource()
	for _, r := range "kerning" {
		m = key(m, string(r))
	}
	if m.lookingUp != "KERNING" {
		t.Fatalf("lookingUp %q", m.lookingUp)
	}
	if !strings.Contains(m.View(), "Searching Linear for kerning…") {
		t.Fatalf("doesn't say it's searching:\n%s", m.View())
	}
	// A space is on its way to the next word: no second search for this one.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = next.(model)
	if cmd := m.startLookup(); cmd != nil || m.lookingUp != "KERNING" {
		t.Fatalf("lookingUp %q, another lookup %v", m.lookingUp, cmd != nil)
	}
}

func TestEscCancelsTheSearch(t *testing.T) {
	m := listModel()
	m.client = newDemoSource()
	for _, r := range "kerning" {
		m = key(m, string(r))
	}
	tick := lookupTickMsg{m.lookingUp, m.gen}
	m = key(m, "esc")
	if next, cmd := m.Update(tick); cmd != nil || next.(model).lookingUp != "" {
		t.Fatalf("searched anyway")
	}
}

func TestListArrivingMeanwhileStopsTheLookup(t *testing.T) {
	m := listModel()
	m.client = newDemoSource()
	for _, r := range "kerning" {
		m = key(m, string(r))
	}
	tick := lookupTickMsg{m.lookingUp, m.gen}
	mine := mkIssue("HAL-1", "started", "In Progress", 3, 0)
	mine.Title = "Kerning"
	next, _ := m.Update(issuesMsg{issues: []issue{mine}})
	next, cmd := next.(model).Update(tick)
	if cmd != nil || next.(model).lookingUp != "" {
		t.Fatalf("asked Linear for what's listed")
	}
}

func TestSearchFailureIsSaid(t *testing.T) {
	m := listModel()
	m.client = failingSearch{newDemoSource()}
	m = typeLookup(m, "kerning")
	if !strings.Contains(m.err, "Couldn't search Linear for KERNING: rate limited") {
		t.Fatalf("err %q", m.err)
	}
}

type failingSearch struct{ *demoSource }

func (failingSearch) searchIssues(context.Context, string) ([]issue, error) {
	return nil, errors.New("rate limited")
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

// listShape is the list as drawn, a word a line: "#" a heading with its
// count, "_" a blank, else the issue's identifier or the project's name.
func listShape(m model) string {
	lines := strings.Split(screenText(m), "\n")
	var shape []string
	for i, r := 0, m.rows(); i < len(r); i++ {
		if m.headed(r, i) {
			if i != m.offset {
				shape = append(shape, "_")
			}
			shape = append(shape, "#"+r[i].group+" "+strings.TrimPrefix(strings.TrimSpace(lines[listTop+len(shape)]), r[i].group+" "))
		}
		name := ""
		if r[i].issue != nil {
			name = r[i].issue.Identifier
		} else {
			name = r[i].project.Name
		}
		if !strings.Contains(lines[listTop+len(shape)], name) {
			return "line " + lines[listTop+len(shape)] + " isn't " + name
		}
		shape = append(shape, name)
	}
	return strings.Join(shape, " ")
}

// Opened and not yet touched, the selection is the top row, whatever a
// fresher list puts there.
func TestSelectionStaysOnTheTopRowUntilYouPickOne(t *testing.T) {
	m := withIDs(twoGroups())
	fresh := withIDs(listModel(
		mkIssue("A-9", "started", "In Review", 3, 0),
		mkIssue("A-2", "started", "In Progress", 2, 0),
		mkIssue("A-1", "started", "In Progress", 2, 0),
	)).issues
	next, _ := m.Update(issuesMsg{issues: fresh})
	if r := next.(model).selected(); r == nil || r.issue.Identifier != "A-9" || next.(model).offset != 0 {
		t.Fatalf("selected %+v, want the top row A-9", r)
	}
}

// Once you've picked one, a fresher list keeps you on it, wherever it went.
func TestSelectionFollowsYourPickThroughARefresh(t *testing.T) {
	m := key(withIDs(twoGroups()), "down") // A-2
	fresh := withIDs(listModel(
		mkIssue("A-9", "started", "In Review", 3, 0),
		mkIssue("A-1", "started", "In Progress", 2, 0),
		mkIssue("A-3", "unstarted", "Todo", 1, 0),
		mkIssue("A-2", "backlog", "Backlog", 0, 0),
	)).issues
	next, _ := m.Update(issuesMsg{issues: fresh})
	m = next.(model)
	if r := m.selected(); r == nil || r.issue.Identifier != "A-2" {
		t.Fatalf("selected %+v, want A-2", r)
	}
	// Gone from the list, it's the top row again.
	next, _ = m.Update(issuesMsg{issues: fresh[:3]})
	if r := next.(model).selected(); r == nil || r.issue.Identifier != "A-9" {
		t.Fatalf("selected %+v, want the top row A-9", r)
	}
}

// Scrolled into a group, its heading is still the first line of the list.
func TestTheGroupsHeadingStaysAtTheTopWhileScrolling(t *testing.T) {
	var is []issue
	for i := 0; i < 40; i++ {
		is = append(is, mkIssue("A-"+strconv.Itoa(i), "unstarted", "Todo", 1, 0))
	}
	m := listModel(is...)
	for i := 0; i < 30; i++ {
		m = key(m, "down")
	}
	lines := strings.Split(screenText(m), "\n")
	if m.offset == 0 || !strings.Contains(lines[listTop], "Todo 40") {
		t.Fatalf("offset %d, first list line %q", m.offset, lines[listTop])
	}
	if r := m.selected(); r.issue.Identifier != "A-30" || !strings.Contains(screenText(m), "A-30 ") {
		t.Fatalf("A-30 isn't on screen:\n%s", screenText(m))
	}
	// Every line the mouse finds a row on is that row's.
	for y, line := range lines {
		if i, ok := m.rowAt(y); ok && !strings.Contains(line, m.rows()[i].issue.Identifier+" ") {
			t.Fatalf("line %d %q isn't row %d", y, line, i)
		}
	}
}

// Too narrow for all a row says at its edge, the title keeps its room and
// the edge gives up the least of it first; narrower still, the tabs shorten.
func TestNarrowPopupsKeepTheTitle(t *testing.T) {
	is := mkIssue("A-1", "started", "In Progress", 0, 1)
	is.Title, is.BranchName, is.Project = "Search index drops accents", "me/a-1", &projectRef{Name: "A project with a long name"}
	m := listModel(is)
	next, _ := m.Update(worktreesMsg{branches: map[string]bool{"me/a-1": true}})
	m = next.(model)
	for _, tc := range []struct {
		width      int
		has, hasnt string
	}{
		{120, "urgent · A project with a long name · worktree", "accents…"},
		{64, "Search index drops accents", "A project"},
		{64, "urgent · worktree", "accents…"},
		{30, "Mine 1", "Linear"},
	} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 20})
		m = next.(model)
		text := screenText(m)
		if !strings.Contains(text, tc.has) || strings.Contains(text, tc.hasnt) {
			t.Errorf("width %d: want %q and no %q:\n%s", tc.width, tc.has, tc.hasnt, text)
		}
		for _, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(line); w > tc.width {
				t.Errorf("width %d: line %d cells wide: %q", tc.width, w, stripANSI(line))
			}
		}
	}
}

// Every tab loads when the popup opens, so each says how many it lists; one
// landing before the tab you're on doesn't pass that one off as empty.
func TestEveryTabSaysHowManyItHasBeforeYouGoToIt(t *testing.T) {
	m := newModel(context.Background(), config{}, "/repo")
	m.width, m.height = 100, 20
	next, _ := m.Update(projectsMsg{projects: []project{{ID: "p1", Name: "Offline sync"}}})
	m = next.(model)
	if text := screenText(m); m.mode != modeLoading || !strings.Contains(text, "Loading") || !strings.Contains(text, "Projects 1") {
		t.Fatalf("mode %v:\n%s", m.mode, text)
	}
	next, _ = m.Update(recentMsg{issues: []issue{mkIssue("A-7", "unstarted", "Todo", 1, 0)}})
	next, _ = next.(model).Update(issuesMsg{issues: twoGroups().issues})
	m = next.(model)
	if text := screenText(m); m.mode != modeList || !strings.Contains(text, "My issues 3  Projects 1  Recent 1") {
		t.Fatalf("mode %v:\n%s", m.mode, text)
	}
}
