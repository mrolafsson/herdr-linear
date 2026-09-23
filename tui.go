package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tab int

const (
	tabMine tab = iota
	tabProjects
)

type mode int

const (
	modeLoading mode = iota
	modeSignedOut
	modeSigningIn
	modeList
	modeBusy
)

// ── messages ──────────────────────────────────────────────────────────────────

type issuesMsg struct {
	issues []issue
	err    error
}
type projectsMsg struct {
	projects []project
	err      error
}
type projectIssuesMsg struct {
	projectID string
	issues    []issue
	err       error
}
type worktreesMsg map[string]bool
type loginStatusMsg string
type loginDoneMsg struct{ err error }
type actionDoneMsg struct {
	err  error
	note string
}

// ── styles ────────────────────────────────────────────────────────────────────

// The defaults, for a theme that leaves a colour unset; useTheme recolours
// the styles from herdr's theme.
var (
	defaultStyleDim      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "243"})
	defaultStyleHeader   = lipgloss.NewStyle().Bold(true)
	defaultStyleTabOn    = lipgloss.NewStyle().Bold(true).Underline(true)
	defaultStyleSelected = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "254", Dark: "237"})
	defaultStyleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#eb5757"))
	defaultStyleUrgent   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f2994a")).Bold(true)
	defaultStyleTree     = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ea7fc"))
	defaultStyleOK       = defaultStyleTree
)

var (
	styleDim      = defaultStyleDim
	styleHeader   = defaultStyleHeader
	styleTabOn    = defaultStyleTabOn
	styleSelected = defaultStyleSelected
	styleErr      = defaultStyleErr
	styleUrgent   = defaultStyleUrgent
	styleTree     = defaultStyleTree
	styleOK       = defaultStyleOK
)

// Linear's status glyphs: an empty ring filling up as work moves along
// (○ ◔ ◕ ●). Every glyph here and in projectIcon is one that common
// monospace fonts such as JetBrains Mono include: a glyph borrowed from a
// fallback font can come out the wrong width and knock the columns askew.
func stateIcon(s workflowState) string {
	switch s.Type {
	case "triage":
		return "◇"
	case "backlog":
		return "◌"
	case "unstarted":
		return "○"
	case "started":
		if strings.Contains(strings.ToLower(s.Name), "review") {
			return "◕"
		}
		return "◔"
	case "completed":
		return "●"
	case "canceled":
		return "⊘"
	}
	return "·"
}

func projectIcon(statusType string) string {
	switch statusType {
	case "backlog":
		return "◌"
	case "planned":
		return "○"
	case "started":
		return "◔"
	case "paused":
		return "‖"
	case "completed":
		return "●"
	case "canceled":
		return "⊘"
	}
	return "·"
}

func colored(glyph, hex string) string {
	if hex == "" {
		return glyph
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render(glyph)
}

// ── model ─────────────────────────────────────────────────────────────────────

type row struct {
	header  string // non-empty: a group heading, not selectable
	spacer  bool   // the blank line above a heading, not selectable
	issue   *issue
	project *project
}

// label rows are there to be read, never selected.
func (r row) label() bool { return r.header != "" || r.spacer }

type model struct {
	ctx       context.Context
	cfg       config
	client    source
	invoked   string // cwd of the space the picker was opened from
	width     int
	height    int
	mode      mode
	tab       tab
	filter    textinput.Model
	spin      spinner.Model
	status    string // busy / sign-in progress line
	err       string // last error, shown above the footer
	issues    []issue
	projects  []project
	drilled   *project // the project whose issues are listed, if any
	projIss   []issue
	loaded    map[tab]bool
	worktrees map[string]bool
	cursor    int
	offset    int
	cancel    context.CancelFunc // cancels an in-flight sign-in

	// Detail screens (detail.go).
	screen      screen
	cur         *issue
	curDetail   *issueDetail
	curProject  *project
	projDetail  *projectDetail
	states      map[string][]workflowState // by team id, fetched once
	stateCursor int
	flash       string // a one-line confirmation, e.g. "Copied …"
	md          *markdown
	scroll      int // first shown line of the detail screen's description

	mouseX, mouseY int // last pointer position; -1 until the mouse moves
}

func newModel(ctx context.Context, cfg config, invoked string) model {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = "filter"
	ti.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	return model{
		ctx: ctx, cfg: cfg, client: &linearClient{cfg: cfg}, invoked: invoked,
		mode: modeLoading, filter: ti, spin: sp, loaded: map[tab]bool{}, worktrees: map[string]bool{},
		states: map[string][]workflowState{}, md: newMarkdown(cfg.Theme != "light"),
		mouseX: -1, mouseY: -1,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.loadIssues(), m.loadWorktrees())
}

func (m model) loadIssues() tea.Cmd {
	return func() tea.Msg {
		is, err := m.client.myIssues(m.ctx)
		return issuesMsg{is, err}
	}
}

func (m model) loadProjects() tea.Cmd {
	return func() tea.Msg {
		ps, err := m.client.projects(m.ctx)
		return projectsMsg{ps, err}
	}
}

func (m model) loadProjectIssues(p project) tea.Cmd {
	return func() tea.Msg {
		is, err := m.client.projectIssues(m.ctx, p.ID)
		return projectIssuesMsg{p.ID, is, err}
	}
}

// loadWorktrees collects the branches that already have a worktree, across the
// invoking repo and every configured one, to mark them in the list.
func (m model) loadWorktrees() tea.Cmd {
	if demo, ok := m.client.(*demoSource); ok {
		return func() tea.Msg { return worktreesMsg(demo.worktreeBranches()) }
	}
	return func() tea.Msg {
		seen := map[string]bool{}
		dirs := []string{m.invoked}
		for _, d := range m.cfg.Repos {
			dirs = append(dirs, d)
		}
		for _, d := range dirs {
			if d == "" {
				continue
			}
			ws, err := listWorktrees(d)
			if err != nil {
				continue
			}
			for _, w := range ws {
				seen[strings.TrimPrefix(w.Branch, "refs/heads/")] = true
			}
		}
		return worktreesMsg(seen)
	}
}

// ── update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, handled := m.updateDetail(msg); handled {
		return next, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.Width = max(10, msg.Width-6)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case issuesMsg:
		if m.handleLoadErr(msg.err) {
			return m, nil
		}
		m.issues, m.loaded[tabMine] = msg.issues, true
		m.mode = modeList
		m.clampCursor()
		return m, nil

	case projectsMsg:
		if m.handleLoadErr(msg.err) {
			return m, nil
		}
		m.projects, m.loaded[tabProjects] = msg.projects, true
		m.mode = modeList
		m.clampCursor()
		return m, nil

	case projectIssuesMsg:
		if m.handleLoadErr(msg.err) {
			return m, nil
		}
		if m.drilled != nil && m.drilled.ID == msg.projectID {
			m.projIss = msg.issues
			m.mode = modeList
			m.cursor, m.offset = 0, 0
			m.clampCursor()
		}
		return m, nil

	case worktreesMsg:
		m.worktrees = msg
		return m, nil

	case loginStatusMsg:
		m.status = string(msg)
		return m, nil

	case loginDoneMsg:
		m.cancel = nil
		if msg.err != nil {
			m.mode, m.err = modeSignedOut, msg.err.Error()
			return m, nil
		}
		m.mode, m.err, m.status = modeLoading, "", ""
		m.loaded = map[tab]bool{}
		return m, m.reload()

	case demoDoneMsg:
		// The demo stays open after an action so you can keep exploring.
		m.mode = modeList
		if msg.branch == "" { // refused: nothing happened
			m.err = msg.note
			return m, nil
		}
		m.flash = msg.note
		m.worktrees[msg.branch] = true
		if msg.state != nil {
			m.applyState(msg.issueID, *msg.state)
		}
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.mode, m.err = modeList, msg.err.Error()
			return m, nil
		}
		if msg.note != "" {
			// Something was left undone: say it here, not in a toast that
			// might never show, and let you close the popup once you've read it.
			m.mode, m.err = modeList, msg.note
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// handleLoadErr routes a failed load: signed out → sign-in screen, else an
// error line over whatever is already listed.
func (m *model) handleLoadErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errTruncated) || errors.Is(err, errIncomplete) {
		// The list is usable, just not complete: show it, and say so.
		m.err = err.Error()
		return false
	}
	if errors.Is(err, errSignedOut) {
		m.mode, m.err = modeSignedOut, ""
	} else {
		m.mode, m.err = modeList, err.Error()
	}
	return true
}

func (m model) reload() tea.Cmd {
	switch {
	case m.drilled != nil:
		return tea.Batch(m.spin.Tick, m.loadProjectIssues(*m.drilled))
	case m.tab == tabProjects:
		return tea.Batch(m.spin.Tick, m.loadProjects())
	default:
		return tea.Batch(m.spin.Tick, m.loadIssues(), m.loadWorktrees())
	}
}

func (m model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	}

	switch m.mode {
	case modeSignedOut:
		switch k.String() {
		case "enter":
			return m.startLogin()
		case "esc", "q":
			return m, tea.Quit
		}
		return m, nil
	case modeSigningIn:
		if k.String() == "esc" && m.cancel != nil {
			m.cancel()
		}
		return m, nil
	case modeBusy, modeLoading:
		if k.String() == "esc" {
			return m, tea.Quit
		}
		// A load in flight doesn't lock the tabs; its reply still lands.
		switch k.String() {
		case "tab", "shift+tab", "left", "right":
			if m.mode == modeBusy || m.screen != screenList {
				return m, nil
			}
		default:
			return m, nil
		}
	}
	if m.screen != screenList {
		return m.handleDetailKey(k)
	}
	m.flash = ""

	switch k.String() {
	case "esc":
		switch {
		case m.filter.Value() != "":
			m.filter.SetValue("")
			m.cursor, m.offset = 0, 0
		case m.drilled != nil:
			m.drilled, m.projIss = nil, nil
			m.cursor, m.offset = 0, 0
		default:
			return m, tea.Quit
		}
		return m, nil
	case "tab", "shift+tab":
		m.drilled, m.projIss, m.err = nil, nil, ""
		if m.tab == tabMine {
			m.tab = tabProjects
		} else {
			m.tab = tabMine
		}
		m.cursor, m.offset = 0, 0
		if !m.loaded[m.tab] {
			m.mode = modeLoading
			return m, m.reload()
		}
		m.mode = modeList // the other tab may still be loading; this one isn't
		m.clampCursor()
		return m, nil
	case "left", "right":
		// With text in the filter, arrows edit it; otherwise they move
		// between tabs, and ← leaves a project's issues.
		if m.filter.Value() != "" {
			break
		}
		switch {
		case k.String() == "left" && m.drilled != nil:
			m.drilled, m.projIss = nil, nil
			m.cursor, m.offset = 0, 0
			m.clampCursor()
		case k.String() == "right" && m.tab == tabMine, k.String() == "left" && m.tab == tabProjects:
			return m.handleKey(keyMsg("tab"))
		}
		return m, nil
	case "up", "ctrl+p", "ctrl+k":
		m.move(-1)
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		m.move(1)
		return m, nil
	case "pgup":
		m.move(-m.listHeight())
		return m, nil
	case "pgdown":
		m.move(m.listHeight())
		return m, nil
	case "ctrl+r":
		m.err = ""
		m.mode = modeLoading
		return m, m.reload()
	case "ctrl+o":
		if r := m.selected(); r != nil {
			u := ""
			if r.issue != nil {
				u = r.issue.URL
			} else if r.project != nil {
				u = r.project.URL
			}
			if u != "" {
				m.openURL(u)
			}
		}
		return m, nil
	case "enter":
		return m.activate(false)
	case "ctrl+s":
		return m.activate(true)
	case "ctrl+w":
		if r := m.selected(); r != nil && r.project != nil {
			return m.runWorktree(projectTarget(*r.project), nil, false)
		}
		return m, nil
	}

	// Anything else edits the filter.
	prev := m.filter.Value()
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(k)
	if m.filter.Value() != prev {
		m.cursor, m.offset = 0, 0
		m.clampCursor()
	}
	return m, cmd
}

func (m model) startLogin() (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel, m.mode, m.err, m.status = cancel, modeSigningIn, "", "Starting sign-in…"
	cfg := m.cfg
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		err := login(ctx, cfg, func(s string) { program.Send(loginStatusMsg(s)) })
		return loginDoneMsg{err}
	})
}

// activate is enter (open the issue or project screen) and ctrl+s (start right
// from the list: In Progress, worktree, and the agent's prompt).
func (m model) activate(start bool) (tea.Model, tea.Cmd) {
	r := m.selected()
	if r == nil {
		return m, nil
	}
	if r.project != nil {
		if start {
			return m, nil
		}
		return m.openProject(*r.project)
	}
	if start {
		is := *r.issue
		return m.runWorktree(issueTarget(is), &is, true)
	}
	return m.openIssue(*r.issue)
}

func (m model) runWorktree(t target, is *issue, start bool) (tea.Model, tea.Cmd) {
	verb := "Opening"
	if !m.worktrees[t.Branch] {
		verb = "Creating worktree for"
	}
	name := t.Label
	if is != nil {
		name = is.Identifier
	}
	m.mode, m.err, m.status = modeBusy, "", verb+" "+name+"…"
	cfg, client, ctx, invoked := m.cfg, m.client, m.ctx, m.invoked
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		if demo, ok := client.(*demoSource); ok {
			return demo.worktree(t, is, start)
		}
		return doWorktree(ctx, cfg, client, invoked, t, is, start)
	})
}

// ── rows + cursor ─────────────────────────────────────────────────────────────

func (m model) rows() []row {
	q := m.filter.Value()
	var rows []row
	addIssues := func(list []issue) {
		last := ""
		for i := range list {
			is := &list[i]
			if !is.matches(q) {
				continue
			}
			if is.State.Name != last {
				if len(rows) > 0 {
					rows = append(rows, row{spacer: true})
				}
				rows = append(rows, row{header: is.State.Name})
				last = is.State.Name
			}
			rows = append(rows, row{issue: is})
		}
	}
	switch {
	case m.drilled != nil:
		addIssues(m.projIss)
	case m.tab == tabMine:
		addIssues(m.issues)
	default:
		for i := range m.projects {
			if p := &m.projects[i]; p.matches(q) {
				rows = append(rows, row{project: p})
			}
		}
	}
	return rows
}

func (m model) selected() *row {
	rows := m.rows()
	if m.cursor >= 0 && m.cursor < len(rows) && !rows[m.cursor].label() {
		return &rows[m.cursor]
	}
	return nil
}

func (m *model) move(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	c := m.cursor + delta
	c = min(max(c, 0), len(rows)-1)
	// Skip headings and spacers, in the direction of travel, then back if we ran off.
	for c >= 0 && c < len(rows) && rows[c].label() {
		c += step
	}
	if c < 0 || c >= len(rows) {
		c = m.cursor
	}
	m.cursor = c
	m.scrollTo()
}

func (m *model) clampCursor() {
	rows := m.rows()
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	for m.cursor < len(rows) && rows[m.cursor].label() {
		m.cursor++
	}
	m.scrollTo()
}

func (m *model) scrollTo() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
		// Keep the group heading above the first row visible.
		if m.offset > 0 {
			if rows := m.rows(); rows[m.offset-1].header != "" {
				m.offset--
			}
		}
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

func (m model) listHeight() int {
	// tabs + filter + blank above; status line + footer below, the footer on
	// the popup's last line (mouse.go counts on that).
	return max(3, m.height-listTop-2)
}

// ── view ──────────────────────────────────────────────────────────────────────

// View is what Bubble Tea writes to the terminal. Every frame passes through
// screenSafe, whatever path its text took to get there.
func (m model) View() string {
	return screenSafe(m.view())
}

func (m model) view() string {
	if m.width == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.viewTabs() + "\n")

	switch m.mode {
	case modeSignedOut:
		b.WriteString("\n  Linear isn't connected yet.\n\n  Press enter to sign in with your browser.\n")
		if m.err != "" {
			b.WriteString("\n  " + styleErr.Render(m.err) + "\n")
		}
		return b.String()
	case modeSigningIn:
		b.WriteString("\n  " + m.spin.View() + " " + m.status + "\n")
		return b.String()
	}

	if m.screen != screenList {
		b.WriteString("\n")
		if m.screen == screenProject {
			b.WriteString(m.viewProject())
		} else {
			b.WriteString(m.viewIssue())
		}
		// Pin the status line and footer to the bottom.
		for n := strings.Count(b.String(), "\n"); n < m.height-2; n++ {
			b.WriteString("\n")
		}
		b.WriteString(m.statusLine())
		b.WriteString(m.renderFooter(m.detailFooter()))
		return b.String()
	}

	b.WriteString(m.filter.View() + "\n\n")
	rows := m.rows()
	h := m.listHeight()
	switch {
	case m.mode == modeLoading && len(rows) == 0:
		b.WriteString("  " + m.spin.View() + " Loading…\n")
		h--
	case len(rows) == 0:
		b.WriteString(styleDim.Render("  Nothing here.") + "\n")
		h--
	}
	for i := m.offset; i < len(rows) && i < m.offset+h; i++ {
		b.WriteString(m.viewRow(rows[i], i == m.cursor) + "\n")
	}
	for i := len(rows) - m.offset; i < h; i++ {
		b.WriteString("\n")
	}

	b.WriteString(m.statusLine())
	b.WriteString(m.renderFooter(m.footer()))
	return b.String()
}

// statusLine is the one line above the footer: work in progress, the last
// error, or a confirmation.
func (m model) statusLine() string {
	switch {
	case m.mode == modeBusy:
		return " " + m.spin.View() + " " + m.status + "\n"
	case m.err != "":
		return " " + styleErr.Render(shorten(clean(m.err, false), max(10, m.width-2))) + "\n"
	case m.flash != "":
		return " " + styleOK.Render("✓ ") + shorten(clean(m.flash, false), max(10, m.width-4)) + "\n"
	}
	return "\n"
}

func (m model) viewTabs() string {
	mine, projs := " My issues ", " Projects "
	if m.tab == tabMine && m.drilled == nil {
		mine = styleTabOn.Render(mine)
		projs = styleDim.Render(projs)
	} else {
		mine = styleDim.Render(mine)
		projs = styleTabOn.Render(projs)
	}
	line := mine + " " + projs
	switch {
	case m.screen == screenProject:
		line += styleDim.Render(" › ") + styleHeader.Render(shorten(m.curProject.Name, 40))
	case m.screen != screenList:
		if m.drilled != nil {
			line += styleDim.Render(" › " + shorten(m.drilled.Name, 30))
		}
		line += styleDim.Render(" › ") + styleHeader.Render(m.cur.Identifier)
	case m.drilled != nil:
		line += styleDim.Render(" › ") + styleHeader.Render(m.drilled.Name)
	}
	return line
}

func (m model) viewRow(r row, selected bool) string {
	w := m.width
	if r.spacer {
		return ""
	}
	if r.header != "" {
		return styleDim.Render("  " + r.header)
	}
	var left, right string
	if r.issue != nil {
		is := r.issue
		urgent := " "
		if is.Priority == 1 {
			urgent = styleUrgent.Render("!")
		}
		left = fmt.Sprintf(" %s %s %-9s ", urgent, colored(stateIcon(is.State), is.State.Color), is.Identifier)
		var meta []string
		if m.drilled != nil && is.Assignee != nil && !is.Assignee.IsMe {
			meta = append(meta, is.Assignee.Name)
		}
		if m.drilled == nil && is.Project != nil {
			meta = append(meta, is.Project.Name)
		}
		right = styleDim.Render(strings.Join(meta, " · "))
		if m.worktrees[issueTarget(*is).Branch] {
			right += " " + styleTree.Render("⌥")
		}
		return m.fitRow(left, is.Title, right, w, selected)
	}
	p := r.project
	lead := " "
	if p.Lead != nil && p.Lead.IsMe {
		lead = styleDim.Render("◆")
	}
	left = fmt.Sprintf(" %s %s ", lead, colored(projectIcon(p.Status.Type), p.Status.Color))
	right = styleDim.Render(fmt.Sprintf("%s · %d%%", p.Status.Name, int(p.Progress*100+0.5)))
	if m.worktrees[projectTarget(*p).Branch] {
		right += " " + styleTree.Render("⌥")
	}
	return m.fitRow(left, p.Name, right, w, selected)
}

// fitRow lays out left + title + right-aligned meta in exactly w cells,
// truncating the title (never the identifier) when space runs out.
func (m model) fitRow(left, title, right string, w int, selected bool) string {
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	room := w - lw - rw - 2
	if room < 8 {
		right, rw = "", 0
		room = w - lw - 1
	}
	title = shorten(title, max(1, room))
	pad := max(1, w-lw-lipgloss.Width(title)-rw-1)
	line := left + title + strings.Repeat(" ", pad) + right
	if selected {
		return highlight(line, w)
	}
	return line
}

// highlight gives a whole row the selection background, w cells wide. The
// row's own coloured pieces each end in a style reset, which would also end
// the background, so it's switched back on after every one.
func highlight(line string, w int) string {
	on := styleSelected.Render("x")
	on = on[:strings.Index(on, "x")] // the escape that turns the background on
	if on != "" {
		line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+on)
	}
	return styleSelected.Width(w).Render(line)
}

func (m model) footer() []hint {
	switch {
	case m.drilled != nil:
		return []hint{{"enter details", "enter"}, {"^s start", "ctrl+s"}, {"^o open in Linear", "ctrl+o"}, {"esc back", "esc"}}
	case m.tab == tabProjects:
		return []hint{{"enter details", "enter"}, {"^w project worktree", "ctrl+w"}, {"^o open in Linear", "ctrl+o"}, {"tab switch", "tab"}, {"esc close", "esc"}}
	default:
		return []hint{{"enter details", "enter"}, {"^s start", "ctrl+s"}, {"^o open in Linear", "ctrl+o"}, {"^r refresh", "ctrl+r"}, {"tab projects", "tab"}, {"esc close", "esc"}}
	}
}

// program lets background work (the sign-in flow) post progress to the UI.
var program *tea.Program

func runPicker(ctx context.Context, cfg config, demo bool) error {
	invoked := os.Getenv("HERDR_LINEAR_CWD")
	// Ask the terminal for its background now: once the program owns stdin,
	// the reply would arrive as stray input.
	if cfg.Theme != "dark" && cfg.Theme != "light" {
		cfg.Theme = "light"
		if lipgloss.HasDarkBackground() {
			cfg.Theme = "dark"
		}
	}
	useTheme(pickerTheme(cfg.Theme == "dark"))
	m := newModel(ctx, cfg, invoked)
	if demo {
		m.client = newDemoSource()
	}
	program = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := program.Run()
	return err
}
