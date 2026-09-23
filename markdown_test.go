package main

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMarkdownIsRenderedNotShownRaw(t *testing.T) {
	md := newMarkdown(true)
	lines := md.lines("k", "## Plan\n\nShip **the brief** today.\n\n- one\n- two\n\n```go\nfmt.Println(1)\n```", 60)
	out := stripStyles(strings.Join(lines, "\n"))
	for _, raw := range []string{"**", "```", "## "} {
		if strings.Contains(out, raw) {
			t.Errorf("raw %q left in:\n%s", raw, out)
		}
	}
	for _, want := range []string{"Plan", "the brief", "one", "fmt.Println(1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if strings.TrimSpace(stripStyles(lines[0])) == "" || strings.TrimSpace(stripStyles(lines[len(lines)-1])) == "" {
		t.Error("blank lines at the edges should be trimmed")
	}
}

func TestMarkdownFitsWidthAndIsCached(t *testing.T) {
	md := newMarkdown(false)
	text := strings.Repeat("word ", 200)
	lines := md.lines("k", text, 50)
	for _, l := range lines {
		if w := len([]rune(stripStyles(l))); w > 50 {
			t.Fatalf("line %d wide > 50: %q", w, stripStyles(l))
		}
	}
	lines[0] = "sentinel"
	if md.lines("k", text, 50)[0] != "sentinel" {
		t.Error("second render at the same width should come from the cache")
	}
	if md.lines("k", text, 70)[0] == "sentinel" {
		t.Error("a new width must re-render")
	}
}

func TestEmptyMarkdownIsNothing(t *testing.T) {
	if l := newMarkdown(true).lines("k", "  \n ", 40); l != nil {
		t.Fatalf("%q", l)
	}
}

func longIssueScreen(t *testing.T) model {
	t.Helper()
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "para%02d\n\n", i)
	}
	next, _ = m.Update(issueDetailMsg{id: "id-A-1", detail: &issueDetail{Description: b.String()}})
	return next.(model)
}

func TestDescriptionScrollsAndStopsAtTheEnd(t *testing.T) {
	m := longIssueScreen(t)
	if !strings.Contains(screenText(m), "para01") || strings.Contains(screenText(m), "para60") {
		t.Fatal("should start at the top")
	}
	m = key(m, "down")
	if strings.Contains(screenText(m), "para01") {
		t.Fatal("down should scroll")
	}
	for i := 0; i < 500; i++ {
		m = key(m, "down")
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "para60") || !strings.Contains(v, "end") {
		t.Fatalf("should stop at the end:\n%s", v)
	}
	if n := strings.Count(m.View(), "\n") + 1; n != m.height {
		t.Fatalf("view is %d lines, popup %d", n, m.height)
	}
	for i := 0; i < 500; i++ {
		m = wheel(m, false)
	}
	if !strings.Contains(screenText(m), "para01") {
		t.Fatal("wheel up should get back to the top")
	}
}

func TestScrollResetsForTheNextIssue(t *testing.T) {
	m := longIssueScreen(t)
	m = key(key(m, "down"), "down")
	m = key(m, "esc")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(model).scroll != 0 {
		t.Fatal("scroll carried over to another issue")
	}
}
