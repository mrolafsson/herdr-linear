package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// listTop is the first list row's line: under the tabs, the filter and a blank.
const listTop = 3

// hint is one footer entry. Clicking it presses its key.
type hint struct {
	label string
	key   string // "" = not clickable
}

const hintSep = " · "

var styleHintHot = lipgloss.NewStyle().Bold(true).Underline(true)

// renderFooter draws the hints dim, with the one under the pointer lit up so
// it reads as clickable.
func (m model) renderFooter(hs []hint) string {
	hot := ""
	if m.mouseY == m.height-1 {
		hot = hintAt(hs, m.mouseX)
	}
	parts := make([]string, len(hs))
	for i, h := range hs {
		if h.key != "" && h.key == hot {
			parts[i] = styleHintHot.Render(h.label)
		} else {
			parts[i] = styleDim.Render(h.label)
		}
	}
	return styleDim.Render(" ") + strings.Join(parts, styleDim.Render(hintSep))
}

// hintAt finds the footer entry under column x, laid out as renderHints does.
func hintAt(hs []hint, x int) string {
	pos := 1
	for _, h := range hs {
		w := lipgloss.Width(h.label)
		if x >= pos && x < pos+w {
			return h.key
		}
		pos += w + lipgloss.Width(hintSep)
	}
	return ""
}

// keyMsg turns a hint's key back into the key press it stands for.
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+w":
		return tea.KeyMsg{Type: tea.KeyCtrlW}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// handleMouse: hovering highlights, one click opens, the wheel scrolls. Footer
// hints and the tabs are buttons.
func (m model) handleMouse(ev tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeSignedOut || m.mode == modeSigningIn {
		return m, nil
	}
	m.mouseX, m.mouseY = ev.X, ev.Y
	if ev.Action == tea.MouseActionMotion {
		// Hover highlights, like a launcher: the click then opens what you see.
		if m.mode == modeBusy || m.mode == modeLoading {
			return m, nil
		}
		if i, ok := m.rowAt(ev.Y); ok {
			m.cursor = i
		} else if i, ok := m.stateAt(ev.Y); ok {
			m.stateCursor = i
		}
		return m, nil
	}
	if ev.Action != tea.MouseActionPress {
		return m, nil
	}
	switch ev.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		delta := 1
		if ev.Button == tea.MouseButtonWheelUp {
			delta = -1
		}
		switch m.screen {
		case screenList:
			m.move(delta)
		case screenIssue, screenProject:
			m.scrollBody(delta)
		case screenStatus:
			if n := len(m.states[m.cur.Team.ID]); n > 0 {
				m.stateCursor = min(max(0, m.stateCursor+delta), n-1)
			}
		}
		return m, nil
	case tea.MouseButtonLeft:
	default:
		return m, nil
	}
	if m.mode == modeBusy {
		return m, nil
	}
	if ev.Y == 0 {
		return m.clickTab(ev.X)
	}
	if m.mode == modeLoading {
		return m, nil
	}

	if ev.Y == m.height-1 {
		hs := m.footer()
		if m.screen != screenList {
			hs = m.detailFooter()
		}
		if k := hintAt(hs, ev.X); k != "" {
			return m.handleKey(keyMsg(k))
		}
		return m, nil
	}

	if i, ok := m.rowAt(ev.Y); ok {
		m.cursor, m.flash = i, ""
		return m.activate(false)
	}
	if i, ok := m.stateAt(ev.Y); ok {
		m.stateCursor = i
		return m.handleDetailKey(keyMsg("enter"))
	}
	return m, nil
}

// rowAt is the selectable list row on screen line y, if any.
func (m model) rowAt(y int) (int, bool) {
	if m.screen != screenList || y < listTop {
		return 0, false
	}
	rows := m.rows()
	i := m.offset + y - listTop
	if i >= len(rows) || i >= m.offset+m.listHeight() || rows[i].label() {
		return 0, false
	}
	return i, true
}

// stateAt is the status picker entry on screen line y, if any.
func (m model) stateAt(y int) (int, bool) {
	if m.screen != screenStatus {
		return 0, false
	}
	header := m.issueHeader()
	room := m.issueBodyRoom(header)
	start := m.pickerStart(room)
	// tabs, blank, header, then the "Move to" heading.
	top := 2 + strings.Count(header, "\n") + 1
	i := start + y - top
	if y < top || i >= len(m.states[m.cur.Team.ID]) || i-start >= max(1, room-1) {
		return 0, false
	}
	return i, true
}

// clickTab switches to the clicked tab, leaving any detail screen or project
// drill-down: the tabs are the way home.
func (m model) clickTab(x int) (tea.Model, tea.Cmd) {
	mineW := lipgloss.Width(" My issues ")
	projW := lipgloss.Width(" Projects ")
	var want tab
	switch {
	case x < mineW:
		want = tabMine
	case x > mineW && x <= mineW+projW:
		want = tabProjects
	default:
		return m, nil
	}
	m.screen, m.err, m.flash = screenList, "", ""
	if want != m.tab {
		return m.handleKey(keyMsg("tab"))
	}
	if m.drilled != nil {
		m.drilled, m.projIss = nil, nil
		m.cursor, m.offset = 0, 0
		m.clampCursor()
	}
	return m, nil
}
