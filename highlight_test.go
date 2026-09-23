package main

import (
	"strings"
	"testing"
)

// cellsWithBackground counts the visible cells of a line drawn while the
// selection background is on.
func cellsWithBackground(line string) (on, total int) {
	bg := styleSelected.Render("x")
	bg = bg[:strings.Index(bg, "x")]
	lit := false
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			j := strings.IndexByte(line[i:], 'm')
			seq := line[i : i+j+1]
			switch {
			case seq == bg || strings.Contains(seq, strings.Trim(bg, "\x1b[m")):
				lit = true
			case seq == "\x1b[0m" || seq == "\x1b[m":
				lit = false
			}
			i += j + 1
			continue
		}
		r := []rune(line[i:])[0]
		i += len(string(r))
		total++
		if lit {
			on++
		}
	}
	return on, total
}

func TestSelectedRowIsHighlightedEdgeToEdge(t *testing.T) {
	m := withIDs(twoGroups())
	m.issues[0].State.Color = "#f2c94c" // coloured icon: a style reset mid-row
	m.issues[0].Priority = 1            // and an urgent marker: another one
	for _, line := range strings.Split(m.View(), "\n") {
		if !strings.Contains(stripANSI(line), "A-1") {
			continue
		}
		on, total := cellsWithBackground(line)
		if total < m.width || on < m.width {
			t.Fatalf("selected row: %d of %d cells highlighted, want all %d", on, total, m.width)
		}
		return
	}
	t.Fatal("selected row not found")
}

func TestUnselectedRowsHaveNoBackground(t *testing.T) {
	m := withIDs(twoGroups())
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(stripANSI(line), "A-2") {
			if on, _ := cellsWithBackground(line); on != 0 {
				t.Fatalf("%d cells highlighted on an unselected row", on)
			}
			return
		}
	}
	t.Fatal("row not found")
}
