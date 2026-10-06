package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// The look the plugins share (look.go); this file is the same in all three.

func TestTerminalThemeTellsRolesApart(t *testing.T) {
	p := spreadTerminal(herdrPalettes["terminal"])
	// herdr's own gives branches and dim text the body's grey, and working
	// the yellow of counts.
	for name, pair := range map[string][2]string{
		"branch and text":    {p.Mauve, p.Subtext0},
		"dim and text":       {p.Overlay0, p.Subtext0},
		"working and counts": {p.Peach, p.Yellow},
		"idle and working":   {p.Green, p.Peach},
		"done and idle":      {p.Teal, p.Green},
	} {
		if pair[0] == pair[1] {
			t.Errorf("%s are both %q", name, pair[0])
		}
	}
}

func TestAPRWearsItsStatesColour(t *testing.T) {
	t.Cleanup(func() { useTheme(nil) })
	p := herdrPalettes["catppuccin"]
	useTheme(&p)
	for name, c := range map[string]struct {
		got  lipgloss.Style
		want string
	}{
		"open":   {prStyle("OPEN", false), p.Green},
		"draft":  {prStyle("OPEN", true), p.Peach}, // work in progress
		"merged": {prStyle("MERGED", false), p.Mauve},
		"closed": {prStyle("CLOSED", true), p.Red},
	} {
		if c.got.GetForeground() != lipgloss.Color(c.want) {
			t.Errorf("%s: %v, want %s", name, c.got.GetForeground(), c.want)
		}
	}
	// A draft is told from a title, from prose and from what's dim.
	for name, s := range map[string]lipgloss.Style{"a title": styleTitle, "prose": styleText, "what's dim": styleDim, "an open PR": prStyle("OPEN", false)} {
		if prStyle("OPEN", true).GetForeground() == s.GetForeground() {
			t.Errorf("a draft is the colour of %s", name)
		}
	}
	if styleBranch.GetForeground() != lipgloss.Color(p.Mauve) || styleText.GetForeground() != lipgloss.Color(p.Subtext0) {
		t.Error("a branch isn't mauve, or prose isn't the quieter text")
	}
}

// A PR is drawn the same wherever it shows: its number in its state's
// colour, its checks and review in theirs. The GitHub plugin's rows and
// another plugin's tokens go through the same styles.
func TestAPRIsDrawnTheSameEverywhere(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.ANSI256); useTheme(nil) })
	p := herdrPalettes["catppuccin"]
	useTheme(&p)
	if got := tokenText("tvh"); got != styleToken.Render("tvh") {
		t.Errorf("a plain token: %q", got)
	}
	for badge, want := range map[string][]string{
		"#482":               {prNumber(482, "OPEN", false)},
		"#148 merged":        {prNumber(148, "MERGED", false), styleMerged.Render("merged")},
		"#12 closed":         {prNumber(12, "CLOSED", false), styleErr.Render("closed")},
		"#476 draft ●":       {prNumber(476, "OPEN", true), styleWorking.Render("draft"), styleWarn.Render("●")},
		"#482 ✓ approved":    {prNumber(482, "OPEN", false), styleOK.Render("✓"), styleOK.Render("approved")},
		"#64 ✗ conflicts":    {prNumber(64, "OPEN", false), styleErr.Render("✗"), styleErr.Render("conflicts")},
		"#9 ✓ changes auto":  {prNumber(9, "OPEN", false), styleErr.Render("changes"), styleTree.Render("auto")},
		"#150 merged #7 ✓":   {prNumber(150, "MERGED", false), prNumber(7, "OPEN", false), styleOK.Render("✓")},
		"#3 ✓ #2 draft ✗ +4": {prNumber(3, "OPEN", false), prNumber(2, "OPEN", true), styleErr.Render("✗"), styleDim.Render("+4")},
	} {
		got := tokenText(badge)
		if ansi.Strip(got) != badge {
			t.Errorf("%q changed its text: %q", badge, ansi.Strip(got))
		}
		for _, piece := range want {
			if !strings.Contains(got, piece) {
				t.Errorf("%q: missing %q in %q", badge, piece, got)
			}
		}
	}
	if got := tokenText("#"); got != styleToken.Render("#") {
		t.Errorf("not a PR after all: %q", got)
	}
}

func TestATitleOutshinesProseWithoutATheme(t *testing.T) {
	t.Cleanup(func() { useTheme(nil) })
	p := spreadTerminal(herdrPalettes["terminal"])
	useTheme(&p)
	old := darkTerminal
	t.Cleanup(func() { darkTerminal = old })
	for dark, want := range map[bool]string{true: "15", false: "0"} {
		darkTerminal = dark
		for name, theme := range map[string]*palette{"the terminal theme": &p, "no theme": nil} {
			useTheme(theme)
			if styleTitle.GetForeground() != lipgloss.Color(want) || styleLead.GetForeground() != lipgloss.Color(want) || styleText.GetForeground() == lipgloss.Color(want) {
				t.Errorf("%s, dark %v: a title should take %s, prose not; got %v", name, dark, want, styleTitle.GetForeground())
			}
		}
	}
	// A theme's own text colour is its brightest: kept.
	dracula := herdrPalettes["dracula"]
	useTheme(&dracula)
	if styleTitle.GetForeground() != lipgloss.Color(dracula.Text) {
		t.Errorf("dracula: %v", styleTitle.GetForeground())
	}
	if !styleTitle.GetBold() || styleLead.GetBold() {
		t.Error("a heading is bold, a row's title isn't")
	}
}

func TestAgentMarks(t *testing.T) {
	for status, glyph := range map[string]string{"blocked": "◉", "working": "◔", "done": "●", "idle": "✓", "": "○", "unknown": "○"} {
		if got := agentMark(status, ""); !strings.Contains(got, glyph) {
			t.Errorf("%q: %q, want %s", status, got, glyph)
		}
	}
	if got := agentMark("working", "◕"); !strings.Contains(got, "◕") {
		t.Errorf("a working agent didn't take the spinner's frame: %q", got)
	}
}

func TestFooterKeysArePills(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.ANSI256); useTheme(nil) })
	p := herdrPalettes["catppuccin"]
	useTheme(&p)
	hs := []hint{{"enter details", "enter", hintGo}, {"↑↓ choose", "", hintView}, {"^w worktree", "ctrl+w", hintAct}, {"esc close", "esc", hintQuiet}}
	line := footerLine(hs, "", 200)
	// As wide as hintAt takes it to be: a cell either side of each label.
	if got := lipgloss.Width(line); got != hintsWidth(hs) {
		t.Fatalf("the line is %d cells, hintAt lays out %d", got, hintsWidth(hs))
	}
	// Each key answers anywhere on its pill, and nowhere else.
	x := 1
	for _, h := range hs {
		w := lipgloss.Width(h.label) + 2
		if hintAt(hs, x, 200) != h.key || hintAt(hs, x+w-1, 200) != h.key {
			t.Errorf("%q isn't under its own pill", h.label)
		}
		if got := hintAt(hs, x+w, 200); got != "" {
			t.Errorf("the gap after %q presses %q", h.label, got)
		}
		x += w + hintGap
	}
	// A pill has a ground in its kind's colour; what isn't a key has none.
	green, peach := "48;2;166;227;161", "48;2;250;179;135"
	if !strings.Contains(line, green) || !strings.Contains(line, peach) {
		t.Errorf("no green or peach ground: %q", line)
	}
	said := line[strings.Index(line, "↑↓")-12 : strings.Index(line, "↑↓")]
	if strings.Contains(said, "48;") {
		t.Errorf("what can't be pressed looks like a button: %q", said)
	}
	if hot := footerLine(hs, "esc", 200); hot == line {
		t.Error("the key under the pointer isn't lit")
	}
	if got := statePill(prStyle("OPEN", true), "Draft"); !strings.Contains(got, " Draft ") || !strings.Contains(got, peach) {
		t.Errorf("a draft's pill isn't on its colour: %q", got)
	}
	if got := statePill(styleDim, "Unknown"); !strings.Contains(got, " Unknown ") || !strings.Contains(got, "48;") {
		t.Errorf("a grey state has no ground to stand on: %q", got)
	}
}

// A popup too narrow for every key says less, then shows fewer; it never
// runs past the edge, and a click still lands on the key that's drawn there.
func TestFooterFitsTheWidth(t *testing.T) {
	hs := []hint{
		{"enter details", "enter", hintGo}, {"^s start", "ctrl+s", hintAct}, {"^c new issue", "ctrl+c", hintAct},
		{"^o open in Linear", "ctrl+o", hintView}, {"^r refresh", "ctrl+r", hintView}, {"tab projects", "tab", hintView},
		{"esc close", "esc", hintQuiet},
	}
	full := hintsWidth(hs)
	for width := full + 5; width >= 12; width-- {
		line := footerLine(hs, "", width)
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("at %d cells the footer is %d wide: %q", width, got, ansi.Strip(line))
		}
		plain := ansi.Strip(line)
		if !strings.Contains(plain, "esc close") {
			t.Fatalf("at %d cells there's no way out shown: %q", width, plain)
		}
		// Where "esc" is drawn is where it's pressed.
		x := lipgloss.Width(plain[:strings.Index(plain, "esc close")])
		if got := hintAt(hs, x, width); got != "esc" {
			t.Fatalf("at %d cells a click on esc presses %q", width, got)
		}
	}
	if got := ansi.Strip(footerLine(hs, "", full)); !strings.Contains(got, "open in Linear") {
		t.Errorf("room for everything, but something was cut: %q", got)
	}
	// Words go before keys do: one cell short, every key is still there.
	short := ansi.Strip(footerLine(hs, "", full-1))
	if strings.Contains(short, "open in Linear") || !strings.Contains(short, "^o open") || !strings.Contains(short, "tab projects") {
		t.Errorf("one cell short: %q", short)
	}
	// Then the keys that only change the view, before the ones that act.
	tight := ansi.Strip(footerLine(hs, "", 60))
	if strings.Contains(tight, "refresh") || !strings.Contains(tight, "^s start") || !strings.Contains(tight, "enter details") {
		t.Errorf("at 60 cells: %q", tight)
	}
}

// A click on a pill presses its key: every key a hint can name comes back as
// the key press of that name, not as its letters typed.
func TestAHintsKeyIsTheKeyItNames(t *testing.T) {
	keys := []string{"enter", "esc", "tab", "shift+tab", "up", "down", "left", "right", "a", "w", "?"}
	for c := 'a'; c <= 'z'; c++ {
		switch c {
		case 'i', 'm', 'j', 'h': // the terminal's own names for tab, enter and backspace
			continue
		}
		keys = append(keys, "ctrl+"+string(c))
	}
	for _, k := range keys {
		if got := keyMsg(k).String(); got != k {
			t.Errorf("keyMsg(%q) presses %q", k, got)
		}
	}
}
