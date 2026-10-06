package main

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Without a terminal, lipgloss drops all styling, so a hovered hint and a
// plain one would render identically and the hover tests would prove nothing.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	os.Exit(m.Run())
}

// lastLine is the raw (styled) last line of the screen: the footer.
func lastLine(m model) string {
	lines := strings.Split(m.View(), "\n")
	return lines[len(lines)-1]
}

func TestHoveredHintLightsUpAndOnlyIt(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	plain := lastLine(m)
	if plain != footerLine(m.detailFooter(), "", m.width) {
		t.Fatal("a key is lit with no pointer on it")
	}

	x, y := locate(t, m, "o open in Linear")
	m = hover(m, x+3, y)
	lit := lastLine(m)
	if lit == plain {
		t.Fatal("hovering a hint should change how it's drawn")
	}
	// Its pill, and only its pill.
	if lit != footerLine(m.detailFooter(), "o", m.width) {
		t.Fatalf("hovered hint not lit, or more than it: %q", lit)
	}
	if stripANSI(lit) != stripANSI(plain) {
		t.Fatal("hover must not change the footer's text or layout")
	}

	// Pointer on the gap between two pills, or off the footer: nothing lit.
	gap := 0
	for gx := 2; gx < m.width; gx++ {
		if hintAt(m.detailFooter(), gx, m.width) == "" && hintAt(m.detailFooter(), gx-1, m.width) != "" {
			gap = gx
			break
		}
	}
	if gap == 0 || lastLine(hover(m, gap, y)) != plain {
		t.Fatal("the gap between two keys lit something up")
	}
	if lastLine(hover(m, x+3, y-3)) != plain {
		t.Fatal("pointer off the footer still lights a hint")
	}
}

func TestHoverFollowsTheFooterAcrossScreens(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	x, y := locate(t, m, "esc back")
	m = hover(m, x, y)
	// Back on the list, the pointer sits over a different hint (or none):
	// the highlight must reflect what's under it now, not the old hint.
	m = key(m, "esc")
	under := hintAt(m.footer(), x, m.width)
	if got := lastLine(m); got != footerLine(m.footer(), under, m.width) {
		t.Fatalf("the footer doesn't light what the pointer is over now (%q): %q", under, got)
	}
}

func TestNoHighlightBeforeTheMouseMoves(t *testing.T) {
	m := withIDs(twoGroups())
	if lastLine(m) != footerLine(m.footer(), "", m.width) {
		t.Fatal("a key is lit with no pointer")
	}
}
