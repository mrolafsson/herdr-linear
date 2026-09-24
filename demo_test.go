package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// demoModel is the picker as `picker --demo` runs it, sized like a popup.
func demoModel(t *testing.T) model {
	t.Helper()
	// Any network or keychain use from the demo fails the test.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("demo made a request to %s", r.URL)
	}))
	oldG, oldT := graphqlURL, tokenURL
	graphqlURL, tokenURL = srv.URL, srv.URL
	oldR := readStore
	readStore = func(string) (*tokens, error) { t.Error("demo read the keychain"); return nil, errSignedOut }
	t.Cleanup(func() { graphqlURL, tokenURL, readStore = oldG, oldT, oldR; srv.Close() })

	m := newModel(context.Background(), config{}, "")
	m.client = newDemoSource()
	m.width, m.height = 100, 30
	return drain(m, m.Init())
}

// drain feeds a command's messages back into the model, like the runtime does,
// skipping the spinner's endless ticks.
func drain(m model, cmd tea.Cmd) model {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if _, tick := msg.(spinner.TickMsg); tick {
			continue
		}
		if msg == nil {
			continue
		}
		next, more := m.Update(msg)
		m = next.(model)
		queue = append(queue, more)
	}
	return m
}

func press(m model, k string) model {
	next, cmd := m.Update(keyMsg(k))
	return drain(next.(model), cmd)
}

func TestDemoListsOnlyYourOpenIssues(t *testing.T) {
	m := demoModel(t)
	if m.mode != modeList || len(m.issues) != 10 {
		t.Fatalf("mode %v, %d issues", m.mode, len(m.issues))
	}
	for _, is := range m.issues {
		if is.Assignee == nil || !is.Assignee.IsMe {
			t.Errorf("%s isn't yours", is.Identifier)
		}
	}
	if m.issues[0].State.Name != "In Review" {
		t.Errorf("work closest to done should come first, got %s", m.issues[0].State.Name)
	}
	if !m.worktrees["sam/hal-231-search-index-drops-accents-in-note-titles"] {
		t.Error("demo worktrees not loaded")
	}
}

func TestDemoProjectShowsTeammatesWork(t *testing.T) {
	m := press(demoModel(t), "tab")
	if len(m.projects) != 5 || m.projects[0].Lead == nil || !m.projects[0].Lead.IsMe {
		t.Fatalf("projects: %+v", m.projects)
	}
	m = press(press(m, "enter"), "i") // Offline sync → its issues
	var others int
	for _, is := range m.projIss {
		if is.Assignee == nil || !is.Assignee.IsMe {
			others++
		}
	}
	if len(m.projIss) != 4 || others != 3 {
		t.Fatalf("%d issues, %d not yours", len(m.projIss), others)
	}
}

func TestDemoStatusChangeSticks(t *testing.T) {
	m := demoModel(t)
	m = press(m, "enter") // HAL-212, In Review
	m = press(press(m, "c"), "down")
	m = press(m, "enter") // → Done
	if m.cur.State.Name != "Done" || m.flash != "Moved to Done" {
		t.Fatalf("state %s flash %q", m.cur.State.Name, m.flash)
	}
	fresh, _ := m.client.myIssues(context.Background())
	for _, is := range fresh {
		if is.Identifier == "HAL-212" {
			t.Fatal("a Done issue should leave My issues on the next load")
		}
	}
}

func TestDemoStartPretendsAndStaysOpen(t *testing.T) {
	m := demoModel(t)
	var todo issue
	for _, is := range m.issues {
		if is.Identifier == "HAL-240" {
			todo = is
		}
	}
	m = press(m, "enter")
	m.cur = &todo
	m = press(m, "s")
	if m.mode != modeList || m.screen != screenIssue {
		t.Fatalf("demo should stay open: mode %v screen %v", m.mode, m.screen)
	}
	if !strings.Contains(m.flash, "Demo: would move HAL-240 to In Progress, create a worktree") {
		t.Fatalf("flash %q", m.flash)
	}
	if m.cur.State.Name != "In Progress" || !m.worktrees[issueTarget(todo).Branch] {
		t.Fatalf("state %s, worktree marked %v", m.cur.State.Name, m.worktrees[issueTarget(todo).Branch])
	}
}

func TestDemoNeverOpensTheBrowser(t *testing.T) {
	old := browse
	browse = func(string) error { t.Error("demo opened a browser"); return nil }
	t.Cleanup(func() { browse = old })
	m := press(demoModel(t), "ctrl+o")
	if !strings.HasPrefix(m.flash, "Demo: would open https://linear.app/halcyon/") {
		t.Fatalf("flash %q", m.flash)
	}
}

// Screenshots come from the demo, so nothing from a real workspace may show
// up on any of its screens.
func TestDemoScreensContainNothingReal(t *testing.T) {
	banned := []string{"ACT-", "Action", "hjortur", "Hjortur", "Olafsson", "olafsson", "mrolafsson"}
	check := func(where string, m model) {
		t.Helper()
		s := screenText(m)
		for _, b := range banned {
			if strings.Contains(s, b) {
				t.Errorf("%s shows %q:\n%s", where, b, s)
			}
		}
	}
	m := demoModel(t)
	check("my issues", m)
	for i := range m.issues {
		d := m
		d.cursor = 0
		d = press(d, "enter")
		d.cur = &m.issues[i]
		d = drain(d, func() tea.Msg {
			det, _ := d.client.issueDetail(context.Background(), m.issues[i].ID)
			return issueDetailMsg{id: m.issues[i].ID, detail: det}
		})
		check("issue "+m.issues[i].Identifier, d)
	}
	p := press(m, "tab")
	check("projects", p)
	p = press(p, "enter")
	check("project page", p)
	check("project issues", press(p, "i"))
}
