package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCleanStripsEscapeSequences(t *testing.T) {
	cases := map[string]string{
		"plain":                             "plain",
		"clip\x1b]52;c;ZXZpbA==\x07board":   "clipboard",   // OSC 52 clipboard write, BEL-terminated
		"title\x1b]0;pwned\x1b\\ok":         "titleok",     // OSC with ST terminator
		"\x1b[2J\x1b[Hfake prompt":          "fake prompt", // clear screen + home
		"red\x1b[31mtext\x1b[0m":            "redtext",     // SGR
		"c1\u009b31mcsi":                    "c1csi",       // 8-bit CSI
		"osc8\x1b]8;;https://evil\x07link":  "osc8link",    // hyperlink
		"reset\x1bcdone":                    "resetdone",   // two-byte ESC c
		"dcs\x1bPq#0;2;0;0;0\x1b\\after":    "dcsafter",    // DCS (sixel)
		"bell\x07 and nul\x00 and del\x7f.": "bell and nul and del.",
		"unterminated\x1b]52;c;AAAA":        "unterminated",
		"trailing esc\x1b":                  "trailing esc",
		"Café ✓ 日本 ◔":                       "Café ✓ 日本 ◔", // printable Unicode survives
	}
	for in, want := range cases {
		if got := clean(in, false); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanLineBreaks(t *testing.T) {
	if got := clean("a\nb\tc\r\nd", false); got != "a b c d" {
		t.Errorf("one-line: %q", got)
	}
	if got := clean("a\nb\tc\r\nd\re", true); got != "a\nb\tc\nde" {
		t.Errorf("prose: %q", got)
	}
}

// End to end: hostile text in every field Linear returns never reaches the
// screen as an escape sequence.
func TestHostileLinearDataRendersInert(t *testing.T) {
	const osc52 = "\x1b]52;c;cm0gLXJmIH4=\x07"
	fakeStore(t, &tokens{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)})
	fakeLinear(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": false},
			"nodes": []any{map[string]any{
				"id": "i1", "identifier": "ENG-1" + osc52, "title": "Fix\x1b[2Jit" + osc52 + "\nnow",
				"branchName": "b", "url": "https://linear.app/x/issue/ENG-1",
				"state":    map[string]any{"id": "s", "name": "In Progress" + osc52, "type": "started", "color": "#f2c94c"},
				"team":     map[string]any{"id": "t", "key": "ENG"},
				"project":  map[string]any{"id": "p", "name": "Proj" + osc52},
				"assignee": map[string]any{"id": "u", "displayName": "Mallory" + osc52, "isMe": true},
			}},
		}}}})
	})
	c := &linearClient{cfg: config{}}
	issues, err := c.myIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), config{}, "")
	m.width, m.height = 100, 20
	next, _ := m.Update(issuesMsg{issues: issues})
	view := next.(model).View()
	for _, bad := range []string{"\x1b]", "\x07", "\x1b[2J", "\n now"} {
		if strings.Contains(view, bad) {
			t.Fatalf("hostile sequence %q reached the screen", bad)
		}
	}
	if is := issues[0]; is.Title != "Fixit now" || is.Identifier != "ENG-1" || is.Assignee.Name != "Mallory" {
		t.Fatalf("cleaned issue: %+v", is)
	}
}

func TestDescriptionKeepsLineBreaksButNotEscapes(t *testing.T) {
	d := &issueDetail{Description: "line one\n\x1b]52;c;AA\x07line two", PriorityLabel: "High\nx"}
	sanitize(d)
	if d.Description != "line one\nline two" || d.PriorityLabel != "High x" {
		t.Fatalf("%q / %q", d.Description, d.PriorityLabel)
	}
}
