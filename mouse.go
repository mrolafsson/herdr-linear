package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// listTop is the first list row's line: under the tabs, the filter and a blank.
const listTop = 3

// renderFooter draws the keys along the bottom, the one under the pointer
// lit so it reads as clickable.
func (m model) renderFooter(hs []hint) string {
	hot := ""
	if m.mouseY == m.height-1 {
		hot = hintAt(hs, m.mouseX, m.width)
	}
	return footerLine(hs, hot, m.width)
}

// handleMouse: hovering highlights, one click opens, the wheel scrolls. Footer
// hints and the tabs are buttons.
func (m model) handleMouse(ev tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeSignedOut || m.mode == modeSigningIn || m.mode == modeChooseWorkspace {
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
		if k := hintAt(hs, ev.X, m.width); k != "" {
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
	want, left := tab(-1), 0
	for t, name := range tabNames {
		w := lipgloss.Width(name)
		if x >= left && x < left+w {
			want = tab(t)
		}
		left += w + 1 // the space between tabs
	}
	if want < 0 {
		return m, nil
	}
	m.screen, m.err, m.flash = screenList, "", ""
	if want != m.tab {
		return m.switchTab(want)
	}
	if m.drilled != nil {
		m.drilled, m.projIss = nil, nil
		m.cursor, m.offset = 0, 0
		m.clampCursor()
	}
	return m, nil
}
