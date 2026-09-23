package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// Round-2 blocker: the Markdown renderer decodes HTML entities, so an escape
// spelled &#27; passes input cleaning and becomes real afterwards. The final
// frame must still carry no escape but colour/style.
func TestEntityEncodedEscapesNeverReachTheScreen(t *testing.T) {
	// The 8-bit OSC goes last: HTML decodes its "terminator" to œ, so the
	// filter rightly drops everything after it rather than guess its end.
	desc := "Clip &#27;]52;c;ZXZpbA==&#7; and `&#x1b;[2J` and &#155;31m done &#x9d;0;t&#x9c;"
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(issueDetailMsg{id: "id-A-1", detail: &issueDetail{Description: desc}})
	frame := next.(model).View()
	if !strings.Contains(stripStyles(frame), "done") {
		t.Fatalf("description not rendered:\n%s", stripStyles(frame))
	}
	for i := 0; i < len(frame); i++ {
		if frame[i] != 0x1b {
			if c := rune(frame[i]); c < 0x20 && c != '\n' {
				t.Fatalf("control byte %#x on screen", c)
			}
			continue
		}
		j := strings.IndexByte(frame[i:], 'm')
		if frame[i+1] != '[' || j < 0 || !sgrParams([]rune(frame[i+2:i+j])) {
			t.Fatalf("non-SGR escape on screen: %q", frame[i:min(len(frame), i+12)])
		}
		i += j
	}
	for _, r := range frame {
		if r >= 0x80 && r <= 0x9f {
			t.Fatalf("C1 control %#x on screen", r)
		}
	}
}

// Round 2b: an entity-encoded *style* escape survives screenSafe (it's valid
// SGR), so it must never be made in the first place. &#27;[8m would hide text.
func TestEntityEncodedStylingIsNotApplied(t *testing.T) {
	// In inline code the renderer keeps the decoded escape whole (in prose
	// it happens to split "[" off, which defuses it there).
	desc := "before `&#27;[8m`hidden after, and `&#x1b;[5m`blink, and a real &amp; and &#169; stay"
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(issueDetailMsg{id: "id-A-1", detail: &issueDetail{Description: desc}})
	frame := next.(model).View()
	for _, sgr := range []string{"\x1b[8m", "\x1b[5m"} {
		if strings.Contains(frame, sgr) {
			t.Fatalf("collaborator-written %q reached the screen", sgr)
		}
	}
	text := stripStyles(frame)
	for _, want := range []string{"hidden", "blink", "&", "©"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q lost:\n%s", want, text)
		}
	}
}

func TestScreenSafeKeepsStylingOnly(t *testing.T) {
	in := "\x1b[1;38;2;255;0;0mbold red\x1b[0m\n\x1b]52;c;AA\x07x\x1b[2Jy\x1b[?25lz\x07"
	if got := screenSafe(in); got != "\x1b[1;38;2;255;0;0mbold red\x1b[0m\nxyz" {
		t.Fatalf("%q", got)
	}
}

func TestDescriptionKeepsLineBreaksButNotEscapes(t *testing.T) {
	d := &issueDetail{Description: "line one\n\x1b]52;c;AA\x07line two", PriorityLabel: "High\nx"}
	sanitize(d)
	if d.Description != "line one\nline two" || d.PriorityLabel != "High x" {
		t.Fatalf("%q / %q", d.Description, d.PriorityLabel)
	}
}
