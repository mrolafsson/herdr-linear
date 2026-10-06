package main

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The look the herdr plugins share. herdr-github, herdr-linear and
// herdr-recap keep this file the same, so that a title, a branch, a pull
// request or a key is drawn alike in all three:
//
//	title    the theme's text at its brightest; bold where it heads a card
//	         or a screen, plain where it's one row among many
//	text     prose (a recap, a description's lead): a step quieter
//	dim      what's read last: where, when, who, the dots in between
//	branch   mauve
//	PR       its state's colour: a draft peach (work in progress, like an
//	         agent that's working), open green (ready), merged mauve,
//	         closed red
//	agent    its state's mark and colour, as herdr's sidebar has it:
//	         ◉ needs you red, ◔ working peach, ● done teal, ✓ idle green
//	counts   yellow; added green, removed red; tasks teal
//	good, bad, waiting   green, red, yellow
//	yours to act on (a worktree, a link, the tab you're on)   the accent
//
// Keys are pills along the bottom, coloured by what pressing one does (see
// hintKind), and the one thing a screen is about wears its state as a pill.

// ── colours ───────────────────────────────────────────────────────────────────

// tones are the colours the styles are made of: herdr's theme, or the
// plugins' own when there's none to follow.
type tones struct {
	Text, Subtext, Dim                                   lipgloss.TerminalColor
	Accent, Red, Green, Yellow, Peach, Blue, Teal, Mauve lipgloss.TerminalColor
	Selection, Surface                                   lipgloss.TerminalColor // backgrounds: the selected row, a quiet pill
	Ink                                                  lipgloss.TerminalColor // text on a coloured pill
}

// ownTones are the plugins' own colours, for a terminal herdr's theme
// doesn't suit (a light theme on a dark terminal, or the other way round).
var ownTones = tones{
	Text: lipgloss.NoColor{}, Subtext: lipgloss.NoColor{}, // Text is brightest(), once the terminal has said
	Dim:    lipgloss.AdaptiveColor{Light: "245", Dark: "243"},
	Accent: lipgloss.Color("#4493f8"), Blue: lipgloss.Color("#4493f8"),
	Red: lipgloss.Color("#f85149"), Green: lipgloss.Color("#3fb950"),
	Yellow: lipgloss.Color("#d29922"), Peach: lipgloss.Color("#db6d28"),
	Teal: lipgloss.Color("#39c5cf"), Mauve: lipgloss.Color("#ab7df8"),
	Selection: lipgloss.AdaptiveColor{Light: "254", Dark: "237"},
	Surface:   lipgloss.AdaptiveColor{Light: "252", Dark: "239"},
	Ink:       lipgloss.Color("#1e1e2e"),
}

// darkTerminal is what the terminal said of its background at startup (or
// what the plugin's config says it is): what's gone by where the theme
// doesn't say, as herdr's "terminal" theme doesn't.
var darkTerminal = true

// brightest is the text colour for a theme that leaves text to the terminal
// (herdr's "terminal" theme, or none): bright white on dark, black on light,
// so a title still stands out from the prose under it.
func brightest() lipgloss.TerminalColor {
	if darkTerminal {
		return lipgloss.Color("15")
	}
	return lipgloss.Color("0")
}

// ink turns a palette colour into one to draw with: "" is the terminal's own.
func ink(c string) lipgloss.TerminalColor {
	if c == "" {
		return lipgloss.NoColor{}
	}
	return lipgloss.Color(c)
}

// tonesOf are a herdr theme's colours. A theme that leaves the selection or
// a surface to the terminal keeps the plugins' own there, so the selected
// row and a quiet pill stay visible.
func tonesOf(p palette) tones {
	t := tones{
		Text: ink(p.Text), Subtext: ink(p.Subtext0), Dim: ink(p.Overlay0),
		Accent: ink(p.Accent), Red: ink(p.Red), Green: ink(p.Green), Yellow: ink(p.Yellow),
		Peach: ink(p.Peach), Blue: ink(p.Blue), Teal: ink(p.Teal), Mauve: ink(p.Mauve),
		Selection: ownTones.Selection, Surface: ownTones.Surface,
	}
	if p.Text == "" {
		t.Text = brightest()
	}
	if p.SelectionBG != "" {
		t.Selection = lipgloss.Color(p.SelectionBG)
	}
	if p.Surface1 != "" {
		t.Surface = lipgloss.Color(p.Surface1)
	}
	// On a pill: the theme's background, which its colours were chosen to
	// stand out from. With none, black or white, whichever the terminal is.
	switch {
	case p.PanelBG != "":
		t.Ink = lipgloss.Color(p.PanelBG)
	case darkTerminal:
		t.Ink = lipgloss.Color("0")
	default:
		t.Ink = lipgloss.Color("15")
	}
	return t
}

// spreadTerminal fits herdr's "terminal" theme to a popup. The theme draws
// with the terminal's 16 colours and, with little of its own to tell apart,
// gives several roles the same one: branches and dim text the body's grey,
// "working" the yellow of counts. A popup has more to tell apart, so those
// take the terminal's other colours. What you set yourself ([theme.custom])
// is applied after, and wins.
func spreadTerminal(p palette) palette {
	p.Mauve = "5"    // magenta, not the text's grey
	p.Yellow = "11"  // bright yellow, leaving plain yellow to peach
	p.Overlay0 = "8" // bright black: dim, as herdr's own sidebar draws it
	return p
}

// ── styles ────────────────────────────────────────────────────────────────────

var (
	look = ownTones

	styleTitle, styleHeader, styleLead, styleText, styleDim lipgloss.Style
	styleTabOn, styleHintHot, styleSelected                 lipgloss.Style
	styleOK, styleErr, styleWarn, styleUrgent               lipgloss.Style
	styleTree, styleMerged, styleBranch                     lipgloss.Style
	// An agent's states, as herdr's sidebar colours them.
	styleBlocked, styleWorking, styleDone, styleIdle lipgloss.Style
	// The details under a title, each kind in its own colour.
	styleModel, styleTasks, styleMode, styleToken lipgloss.Style
)

func init() { useTheme(nil) }

// theme is herdr's palette when the plugin follows it, nil when it draws in
// its own colours. Markdown is coloured from it.
var theme *palette

// useTheme recolours everything with p, or with nil restores the plugins'
// own colours.
func useTheme(p *palette) {
	theme = p
	if p == nil {
		t := ownTones
		t.Text = brightest()
		useTones(t)
		return
	}
	useTones(tonesOf(*p))
}

func useTones(t tones) {
	look = t
	fg := func(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	styleTitle = fg(t.Text).Bold(true)
	styleHeader = styleTitle
	styleLead = fg(t.Text)
	styleText = fg(t.Subtext)
	styleDim = fg(t.Dim)
	styleTabOn = fg(t.Accent).Bold(true).Underline(true)
	styleHintHot = fg(t.Accent).Bold(true).Underline(true)
	styleSelected = lipgloss.NewStyle().Background(t.Selection)
	styleOK, styleErr, styleWarn = fg(t.Green), fg(t.Red), fg(t.Yellow)
	styleUrgent = fg(t.Peach).Bold(true)
	styleTree = fg(t.Accent)
	styleMerged, styleBranch = fg(t.Mauve), fg(t.Mauve)
	styleBlocked, styleWorking, styleDone, styleIdle = fg(t.Red), fg(t.Peach), fg(t.Teal), fg(t.Green)
	styleModel, styleTasks, styleMode, styleToken = fg(t.Blue), fg(t.Teal), fg(t.Yellow), fg(t.Green)
}

// prStyle is the colour of a pull request, by its state: its number, its
// mark and its pill all wear it.
func prStyle(state string, draft bool) lipgloss.Style {
	switch {
	case state == "MERGED":
		return styleMerged
	case state == "CLOSED":
		return styleErr
	case draft:
		// Still being worked on: the colour of a working agent. GitHub's
		// grey would be lost among the titles, or among what's dim.
		return styleWorking
	}
	return styleOK
}

// prNumber is a pull request's number as it's drawn everywhere, in every
// plugin: "#482" in its state's colour.
func prNumber(number int, state string, draft bool) string {
	return prStyle(state, draft).Render("#" + strconv.Itoa(number))
}

// tokenText colours a herdr token's text. A pull request's badge ("#482 ✓
// approved", or several: "#12 draft ✓ #9 ● +2") is drawn as the GitHub
// plugin draws a PR: the number and its state in the state's colour, its
// checks and its review in theirs. Anything else is a token.
func tokenText(v string) string {
	if len(v) < 2 || v[0] != '#' || v[1] < '0' || v[1] > '9' {
		return styleToken.Render(v)
	}
	var out []string
	for i, pr := range strings.Split(v, " #") {
		if i > 0 {
			pr = "#" + pr
		}
		state := "OPEN"
		switch {
		case strings.Contains(pr, "merged"):
			state = "MERGED"
		case strings.Contains(pr, "closed"):
			state = "CLOSED"
		}
		own := prStyle(state, strings.Contains(pr, "draft"))
		for _, word := range strings.Fields(pr) {
			style := own // the number, and "draft", "merged", "closed"
			switch {
			case word == "✓" || word == "approved":
				style = styleOK
			case word == "✗" || word == "changes" || word == "conflicts":
				style = styleErr
			case word == "●":
				style = styleWarn
			case word == "auto":
				style = styleTree
			case strings.HasPrefix(word, "+"):
				style = styleDim // "+2": more of them, not shown
			}
			out = append(out, style.Render(word))
		}
	}
	return strings.Join(out, " ")
}

// agentMark is an agent's state, as herdr's sidebar marks it. working is the
// frame of a spinner to show while it works, or "" for a still mark.
func agentMark(status, working string) string {
	switch status {
	case "blocked":
		return styleBlocked.Render("◉")
	case "working":
		if working == "" {
			working = "◔"
		}
		return styleWorking.Render(working)
	case "done":
		return styleDone.Render("●")
	case "idle":
		return styleIdle.Render("✓")
	}
	return styleDim.Render("○")
}

// ── pills ─────────────────────────────────────────────────────────────────────

// pill is text on a coloured ground, a cell of it either side: a key along
// the bottom, or the state of the thing a screen is about.
func pill(ground lipgloss.TerminalColor, text string) string {
	return lipgloss.NewStyle().Background(ground).Foreground(look.Ink).Bold(true).Render(" " + text + " ")
}

// statePill is a pill in the colour of a style (prStyle, an agent's state).
// Grey makes no ground to stand out on, so a grey state sits on the quiet
// surface.
func statePill(s lipgloss.Style, text string) string {
	if fg := s.GetForeground(); fg == look.Dim || fg == look.Subtext {
		return lipgloss.NewStyle().Background(look.Surface).Foreground(look.Text).Render(" " + text + " ")
	}
	return pill(s.GetForeground(), text)
}

// hintKind is what pressing a key does, which is what its pill is coloured
// by, so the keys read in groups before they're read one by one.
type hintKind int

const (
	hintGo    hintKind = iota // the thing to do here: open it, go there, choose it
	hintAct                   // changes something: a worktree, a merge, a reply
	hintView                  // changes what you see: another tab, a refresh, a copy
	hintQuiet                 // leaves: back, close, cancel
	hintRisk                  // can't be undone
)

// hint is one key along the bottom. Its label starts with the key ("^w
// worktree"). Clicking it presses its key; with no key it's only said.
type hint struct {
	label string
	key   string // "" = not clickable
	kind  hintKind
}

// hintGap is the space between two pills.
const hintGap = 1

// hintsWidth is how many cells a line of pills takes.
func hintsWidth(hs []hint) int {
	w := 1
	for i, h := range hs {
		w += lipgloss.Width(h.label) + 2
		if i > 0 {
			w += hintGap
		}
	}
	return w
}

// fitHints makes the keys fit a line width cells wide. First by saying less:
// a key's words down to the first ("o open in Linear" becomes "o open"),
// from the right. Then by showing fewer, the least missed first: keys that
// only change the view, then those that act, then those that go. What leaves
// (esc) always stays. A key that isn't shown still works.
func fitHints(hs []hint, width int) []hint {
	if width <= 0 || hintsWidth(hs) <= width {
		return hs
	}
	out := append([]hint(nil), hs...)
	for i := len(out) - 1; i >= 0 && hintsWidth(out) > width; i-- {
		key, what, _ := strings.Cut(out[i].label, " ")
		if first, _, more := strings.Cut(what, " "); more {
			out[i].label = key + " " + first
		}
	}
	for _, kind := range []hintKind{hintView, hintAct, hintGo} {
		for i := len(out) - 1; i >= 0 && hintsWidth(out) > width; i-- {
			if out[i].kind == kind {
				out = append(out[:i], out[i+1:]...)
			}
		}
	}
	return out
}

// footerLine draws the keys as pills, fitted to width, the one under the
// pointer (hot, by its key) underlined so it reads as about to be pressed.
func footerLine(hs []hint, hot string, width int) string {
	hs = fitHints(hs, width)
	parts := make([]string, len(hs))
	for i, h := range hs {
		key, what, _ := strings.Cut(h.label, " ")
		if h.key == "" {
			// Not a button: said, without a ground to press.
			parts[i] = styleDim.Render(" " + h.label + " ")
			continue
		}
		ground, on := look.Surface, look.Text
		switch h.kind {
		case hintGo:
			ground, on = look.Green, look.Ink
		case hintAct:
			ground, on = look.Peach, look.Ink
		case hintView:
			ground, on = look.Blue, look.Ink
		case hintRisk:
			ground, on = look.Red, look.Ink
		}
		s := lipgloss.NewStyle().Background(ground).Foreground(on)
		if h.key == hot {
			s = s.Underline(true)
		}
		text := s.Bold(true).Render(" " + key)
		if what != "" {
			text += s.Render(" " + what)
		}
		parts[i] = text + s.Render(" ")
	}
	return " " + strings.Join(parts, strings.Repeat(" ", hintGap))
}

// hintAt finds the key under column x, laid out as footerLine does at width.
func hintAt(hs []hint, x, width int) string {
	pos := 1
	for _, h := range fitHints(hs, width) {
		w := lipgloss.Width(h.label) + 2
		if x >= pos && x < pos+w {
			return h.key
		}
		pos += w + hintGap
	}
	return ""
}

// keyMsg turns a hint's key back into the key press it stands for, so that a
// click on a pill does what the key does: "enter", "esc", "left", "ctrl+w",
// or a letter.
func keyMsg(k string) tea.KeyMsg {
	named := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	}
	if t, ok := named[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	// ctrl+a … ctrl+z are the control characters 1 … 26, as the terminal
	// sends them.
	if c, ok := strings.CutPrefix(k, "ctrl+"); ok && len(c) == 1 && c[0] >= 'a' && c[0] <= 'z' {
		return tea.KeyMsg{Type: tea.KeyType(c[0] - 'a' + 1)}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}
