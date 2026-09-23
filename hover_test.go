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

// footerLine is the raw (styled) last line of the screen.
func footerLine(m model) string {
	lines := strings.Split(m.View(), "\n")
	return lines[len(lines)-1]
}

func TestHoveredHintLightsUpAndOnlyIt(t *testing.T) {
	m := withIDs(twoGroups())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	plain := footerLine(m)

	x, y := locate(t, m, "o open in Linear")
	m = hover(m, x+3, y)
	lit := footerLine(m)
	if lit == plain {
		t.Fatal("hovering a hint should change how it's drawn")
	}
	if want := styleHintHot.Render("o open in Linear"); !strings.Contains(lit, want) {
		t.Fatalf("hovered hint not lit: %q", lit)
	}
	if strings.Contains(lit, styleHintHot.Render("w worktree")) {
		t.Fatal("only the hovered hint should light up")
	}
	if stripANSI(lit) != stripANSI(plain) {
		t.Fatal("hover must not change the footer's text or layout")
	}

	// Pointer on the separator, or off the footer: nothing lit.
	sx, _ := locate(t, m, "·")
	if footerLine(hover(m, sx, y)) != plain {
		t.Fatal("separator lit something up")
	}
	if footerLine(hover(m, x+3, y-3)) != plain {
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
	under := hintAt(m.footer(), x)
	for _, h := range m.footer() {
		lit := strings.Contains(footerLine(m), styleHintHot.Render(h.label))
		if lit != (h.key != "" && h.key == under) {
			t.Fatalf("hint %q lit=%v, pointer is over %q", h.label, lit, under)
		}
	}
}

func TestNoHighlightBeforeTheMouseMoves(t *testing.T) {
	m := withIDs(twoGroups())
	for _, h := range m.footer() {
		if strings.Contains(footerLine(m), styleHintHot.Render(h.label)) {
			t.Fatalf("%q lit with no pointer", h.label)
		}
	}
}
