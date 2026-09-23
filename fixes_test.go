package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestShortenCountsCells(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"日本語のタイトル", 7, "日本語…"}, // 6 cells of text + the ellipsis
		{"🚀🚀🚀🚀", 5, "🚀🚀…"},
		{"abc", 0, ""},
	}
	for _, c := range cases {
		got := shorten(c.in, c.n)
		if got != c.want || lipgloss.Width(got) > c.n {
			t.Errorf("shorten(%q, %d) = %q (%d cells), want %q", c.in, c.n, got, lipgloss.Width(got), c.want)
		}
	}
}

func TestOnlyLinearLinksOpen(t *testing.T) {
	for u, ok := range map[string]bool{
		"https://linear.app/acme/issue/ENG-1":     true,
		"https://uploads.linear.app/a/b.png":      true,
		"http://linear.app/acme/issue/ENG-1":      false, // not https
		"https://linear.app.evil.com/":            false,
		"https://evil.com/?linear.app":            false,
		"https://user@linear.app/":                false,
		"file:///Applications/Calculator.app":     false,
		"x-apple.systempreferences:com.apple.xyz": false,
		"-a Calculator":                           false,
		"":                                        false,
	} {
		if isLinearURL(u) != ok {
			t.Errorf("isLinearURL(%q) = %v", u, !ok)
		}
	}
}

func TestEditedDescriptionReRenders(t *testing.T) {
	md := newMarkdown(true)
	a := strings.Join(md.lines("i1", "first version", 40), "")
	b := strings.Join(md.lines("i1", "other version", 40), "") // same length
	if stripStyles(a) == stripStyles(b) {
		t.Fatal("a same-length edit was served from the cache")
	}
}

// Two teams number "In Progress" differently; it must still be one group.
func TestOneStatusStaysOneGroupAcrossTeams(t *testing.T) {
	is := []issue{
		mkIssue("API-1", "started", "In Progress", 2, 0),
		mkIssue("WEB-1", "started", "In Review", 3, 0),
		mkIssue("WEB-2", "started", "In Progress", 5, 0), // WEB numbers states higher
	}
	sortIssues(is)
	var groups []string
	for i, x := range is {
		if i == 0 || x.State.Name != is[i-1].State.Name {
			groups = append(groups, x.State.Name)
		}
	}
	if len(groups) != 2 {
		t.Fatalf("groups %v from %v", groups, is)
	}
}
