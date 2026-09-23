package main

import (
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	screenList screen = iota
	screenIssue
	screenProject
	screenStatus // the status picker, over the issue screen
)

type issueDetailMsg struct {
	id     string
	detail *issueDetail
	err    error
}
type projectDetailMsg struct {
	id     string
	detail *projectDetail
	err    error
}
type statesMsg struct {
	teamID string
	states []workflowState
	err    error
}
type stateSetMsg struct {
	issueID string
	state   workflowState
	err     error
}

// ── opening ───────────────────────────────────────────────────────────────────

func (m model) openIssue(is issue) (tea.Model, tea.Cmd) {
	m.screen, m.cur, m.curDetail, m.err, m.flash, m.scroll = screenIssue, &is, nil, "", "", 0
	client, ctx := m.client, m.ctx
	return m, func() tea.Msg {
		d, err := client.issueDetail(ctx, is.ID)
		return issueDetailMsg{is.ID, d, err}
	}
}

func (m model) openProject(p project) (tea.Model, tea.Cmd) {
	m.screen, m.curProject, m.projDetail, m.err, m.flash, m.scroll = screenProject, &p, nil, "", "", 0
	client, ctx := m.client, m.ctx
	return m, func() tea.Msg {
		d, err := client.projectDetail(ctx, p.ID)
		return projectDetailMsg{p.ID, d, err}
	}
}

func (m model) openStatusPicker() (tea.Model, tea.Cmd) {
	m.screen, m.err, m.flash = screenStatus, "", ""
	if len(m.states[m.cur.Team.ID]) > 0 {
		m.placeStateCursor()
		return m, nil
	}
	teamID, client, ctx := m.cur.Team.ID, m.client, m.ctx
	return m, func() tea.Msg {
		s, err := client.teamStates(ctx, teamID)
		return statesMsg{teamID, s, err}
	}
}

func (m *model) placeStateCursor() {
	m.stateCursor = 0
	for i, s := range m.states[m.cur.Team.ID] {
		if s.ID == m.cur.State.ID {
			m.stateCursor = i
		}
	}
}

// ── messages ──────────────────────────────────────────────────────────────────

func (m model) updateDetail(msg tea.Msg) (model, bool) {
	switch msg := msg.(type) {
	case issueDetailMsg:
		if m.cur != nil && m.cur.ID == msg.id {
			if !m.handleLoadErr(msg.err) {
				m.curDetail = msg.detail
			}
		}
		return m, true
	case projectDetailMsg:
		if m.curProject != nil && m.curProject.ID == msg.id {
			if !m.handleLoadErr(msg.err) {
				m.projDetail = msg.detail
			}
		}
		return m, true
	case statesMsg:
		if m.handleLoadErr(msg.err) {
			m.screen = screenIssue
			return m, true
		}
		m.states[msg.teamID] = msg.states
		if m.screen == screenStatus && m.cur != nil && m.cur.Team.ID == msg.teamID {
			m.placeStateCursor()
		}
		return m, true
	case stateSetMsg:
		m.mode = modeList
		if m.handleLoadErr(msg.err) {
			return m, true
		}
		m.applyState(msg.issueID, msg.state)
		m.flash = "Moved to " + msg.state.Name
		return m, true
	}
	return m, false
}

// applyState updates the issue everywhere it is listed, so the lists are right
// without a refetch.
func (m *model) applyState(issueID string, s workflowState) {
	for _, list := range [][]issue{m.issues, m.projIss} {
		for i := range list {
			if list[i].ID == issueID {
				list[i].State = s
			}
		}
		sortIssues(list)
	}
	if m.cur != nil && m.cur.ID == issueID {
		m.cur.State = s
	}
}

// ── keys ──────────────────────────────────────────────────────────────────────

func (m model) handleDetailKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenStatus:
		states := m.states[m.cur.Team.ID]
		switch k.String() {
		case "esc", "c":
			m.screen = screenIssue
		case "up", "k", "ctrl+p":
			m.stateCursor = max(0, m.stateCursor-1)
		case "down", "j", "ctrl+n":
			m.stateCursor = min(len(states)-1, m.stateCursor+1)
		case "enter":
			if m.stateCursor < len(states) {
				s := states[m.stateCursor]
				m.screen = screenIssue
				if s.ID == m.cur.State.ID {
					return m, nil
				}
				m.mode, m.status = modeBusy, "Moving "+m.cur.Identifier+" to "+s.Name+"…"
				id, client, ctx := m.cur.ID, m.client, m.ctx
				return m, tea.Batch(m.spin.Tick, func() tea.Msg {
					return stateSetMsg{id, s, client.setState(ctx, id, s.ID)}
				})
			}
		}
		return m, nil

	case screenIssue, screenProject:
		switch k.String() {
		case "up", "k", "ctrl+p":
			m.scrollBody(-1)
			return m, nil
		case "down", "j", "ctrl+n":
			m.scrollBody(1)
			return m, nil
		case "pgup":
			m.scrollBody(-max(1, m.bodyRoom()-2))
			return m, nil
		case "pgdown", " ":
			m.scrollBody(max(1, m.bodyRoom()-2))
			return m, nil
		}
	}

	switch m.screen {
	case screenIssue:
		is := *m.cur
		switch k.String() {
		case "esc", "q", "left", "h":
			m.screen, m.err, m.flash = screenList, "", ""
		case "o":
			m.openURL(is.URL)
		case "w", "enter":
			return m.runWorktree(issueTarget(is), &is, false)
		case "s":
			return m.runWorktree(issueTarget(is), &is, true)
		case "c":
			return m.openStatusPicker()
		case "y":
			branch := issueTarget(is).Branch
			if err := copyText(branch); err != nil {
				m.err = err.Error()
			} else {
				m.flash = "Copied " + branch
			}
		}
		return m, nil

	case screenProject:
		p := *m.curProject
		switch k.String() {
		case "esc", "q", "left", "h":
			m.screen, m.err, m.flash = screenList, "", ""
		case "o":
			m.openURL(p.URL)
		case "w":
			return m.runWorktree(projectTarget(p), nil, false)
		case "i", "enter":
			m.screen = screenList
			m.drilled, m.projIss = &p, nil
			m.filter.SetValue("")
			m.cursor, m.offset, m.mode = 0, 0, modeLoading
			return m, tea.Batch(m.spin.Tick, m.loadProjectIssues(p))
		}
		return m, nil
	}
	return m, nil
}

// openURL opens Linear in the browser. The demo's workspace doesn't exist, so
// it only says where it would have gone.
func (m *model) openURL(u string) {
	if _, demo := m.client.(*demoSource); demo {
		m.flash = "Demo: would open " + u
		return
	}
	if err := openBrowser(u); err != nil {
		m.err = "Couldn't open the browser: " + err.Error()
	}
}

func copyText(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

// ── views ─────────────────────────────────────────────────────────────────────

func field(name, value string) string {
	if value == "" {
		return ""
	}
	return " " + styleDim.Render(fmt.Sprintf("%-9s", name)) + " " + value + "\n"
}

func (m model) viewIssue() string {
	header := m.issueHeader()
	var b strings.Builder
	b.WriteString(header)
	room := m.issueBodyRoom(header)
	switch {
	case m.screen == screenStatus:
		b.WriteString(m.viewStatusPicker(room))
	case m.curDetail == nil:
		b.WriteString(" " + m.spin.View() + styleDim.Render(" loading…") + "\n")
	default:
		b.WriteString(window(m.bodyLines(), m.scroll, room))
	}
	return b.String()
}

// issueBodyRoom is how many lines are left under the header for the
// description or the status picker.
func (m model) issueBodyRoom(header string) int {
	used := strings.Count(header, "\n") + 2 // + tabs line + blank line
	return m.height - used - 2              // status line + footer
}

// bodyLines is the rendered Markdown under the current detail screen's header.
func (m model) bodyLines() []string {
	w := max(20, m.width)
	switch {
	case m.screen == screenProject && m.projDetail != nil:
		text := m.projDetail.Content
		if strings.TrimSpace(text) == "" {
			text = m.projDetail.Description
		}
		return m.md.lines(m.curProject.ID, text, w)
	case m.screen == screenIssue && m.curDetail != nil:
		return m.md.lines(m.cur.ID, m.curDetail.Description, w)
	}
	return nil
}

func (m model) bodyRoom() int {
	if m.screen == screenProject {
		return m.issueBodyRoom(m.projectHeader())
	}
	return m.issueBodyRoom(m.issueHeader())
}

func (m *model) scrollBody(delta int) {
	m.scroll = min(max(0, m.scroll+delta), maxScroll(m.bodyLines(), m.bodyRoom()))
}

// issueHeader is everything on the issue screen above the description.
func (m model) issueHeader() string {
	is := m.cur
	w := max(20, m.width-2)
	var b strings.Builder

	head := " " + colored(stateIcon(is.State), is.State.Color) + " " + is.State.Name
	if m.curDetail != nil && is.Priority > 0 && m.curDetail.PriorityLabel != "" {
		p := m.curDetail.PriorityLabel
		if is.Priority == 1 {
			p = styleUrgent.Render("! " + p)
		}
		head += styleDim.Render("  ·  ") + p
	}
	b.WriteString(head + "\n\n")
	b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(1).Bold(true).Render(is.Title) + "\n\n")

	if is.Project != nil {
		b.WriteString(field("Project", is.Project.Name))
	}
	if is.Assignee != nil {
		name := is.Assignee.Name
		if is.Assignee.IsMe {
			name += styleDim.Render(" (you)")
		}
		b.WriteString(field("Assignee", name))
	} else {
		b.WriteString(field("Assignee", styleDim.Render("nobody")))
	}
	if d := m.curDetail; d != nil {
		var labels []string
		for _, l := range d.Labels.Nodes {
			labels = append(labels, colored("●", l.Color)+" "+l.Name)
		}
		b.WriteString(field("Labels", strings.Join(labels, "  ")))
		if d.Cycle != nil {
			c := fmt.Sprintf("Cycle %d", d.Cycle.Number)
			if d.Cycle.Name != "" {
				c += " · " + d.Cycle.Name
			}
			b.WriteString(field("Cycle", c))
		}
		b.WriteString(field("Due", d.DueDate))
	}
	t := issueTarget(*is)
	tree := styleDim.Render("no worktree yet")
	if m.worktrees[t.Branch] {
		tree = styleTree.Render("⌥ worktree")
	}
	b.WriteString(field("Branch", t.Branch+"  "+tree))
	b.WriteString("\n")
	return b.String()
}

// pickerStart is the first state shown when a team has more states than fit.
func (m model) pickerStart(room int) int {
	if room > 1 && m.stateCursor >= room-1 {
		return m.stateCursor - room + 2
	}
	return 0
}

func (m model) viewStatusPicker(room int) string {
	states := m.states[m.cur.Team.ID]
	if len(states) == 0 {
		return " " + m.spin.View() + styleDim.Render(" loading statuses…") + "\n"
	}
	var b strings.Builder
	b.WriteString(styleHeader.Render(" Move to") + "\n")
	start := m.pickerStart(room)
	for i := start; i < len(states) && i-start < max(1, room-1); i++ {
		s := states[i]
		line := "   " + colored(stateIcon(s), s.Color) + " " + s.Name
		if s.ID == m.cur.State.ID {
			line += styleDim.Render("  current")
		}
		if i == m.stateCursor {
			line = highlight(" ›"+line[2:], max(20, m.width-2))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (m model) viewProject() string {
	header := m.projectHeader()
	if m.projDetail == nil {
		return header + " " + m.spin.View() + styleDim.Render(" loading…") + "\n"
	}
	return header + window(m.bodyLines(), m.scroll, m.issueBodyRoom(header))
}

func (m model) projectHeader() string {
	p := m.curProject
	w := max(20, m.width-2)
	var b strings.Builder
	b.WriteString(" " + colored(projectIcon(p.Status.Type), p.Status.Color) + " " + p.Status.Name +
		styleDim.Render(fmt.Sprintf("  ·  %d%%", int(p.Progress*100+0.5))) + "\n\n")
	b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(1).Bold(true).Render(p.Name) + "\n\n")
	if d := m.projDetail; d != nil {
		if d.Lead != nil {
			b.WriteString(field("Lead", d.Lead.Name))
		}
		b.WriteString(field("Start", d.StartDate))
	}
	b.WriteString(field("Target", p.TargetDate))
	t := projectTarget(*p)
	tree := styleDim.Render("no worktree yet")
	if m.worktrees[t.Branch] {
		tree = styleTree.Render("⌥ worktree")
	}
	b.WriteString(field("Branch", t.Branch+"  "+tree))
	b.WriteString("\n")
	return b.String()
}

func (m model) detailFooter() []hint {
	switch m.screen {
	case screenStatus:
		return []hint{{"↑↓ choose", ""}, {"enter move", "enter"}, {"esc cancel", "esc"}}
	case screenProject:
		return []hint{{"i issues", "i"}, {"w worktree", "w"}, {"o open in Linear", "o"}, {"esc back", "esc"}}
	default:
		return []hint{{"w worktree", "w"}, {"s start", "s"}, {"c status", "c"}, {"o open in Linear", "o"}, {"y copy branch", "y"}, {"esc back", "esc"}}
	}
}
