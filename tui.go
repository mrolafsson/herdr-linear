package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type tab int

const (
	tabMine tab = iota
	tabProjects
	tabRecent // the issues you created lately, to find what you just filed
	tabCount
)

type mode int

const (
	modeLoading mode = iota
	modeSignedOut
	modeSigningIn
	modeList
	modeBusy
	modeChooseWorkspace
)

// ── messages ──────────────────────────────────────────────────────────────────

// A load's reply carries the gen it was asked for under, so one that
// arrives after switching workspace is dropped, not shown as the new one's.
type issuesMsg struct {
	issues []issue
	err    error
	gen    int
}
type recentMsg struct {
	issues []issue
	err    error
	gen    int
}
type projectsMsg struct {
	projects []project
	err      error
	gen      int
}
type projectIssuesMsg struct {
	projectID string
	issues    []issue
	err       error
	gen       int
}
type worktreesMsg struct {
	branches map[string]bool
	gen      int
}
type loginStatusMsg string
type loginLinkMsg struct {
	url    string
	opened bool
}
type loginDoneMsg struct {
	ws  workspace
	err error
}

// lookupTickMsg fires once typing pauses on a filter worth asking Linear
// about (startLookup); lookupMsg is Linear's answer (findIssues).
type lookupTickMsg struct {
	query string
	gen   int
}
type lookupMsg struct {
	query  string
	issues []issue
	err    error
	gen    int
}

// noteMsg puts a line in the status line without stopping anything.
type noteMsg string

type workspacesMsg struct {
	index workspaceIndex
	err   error
}
type actionDoneMsg struct {
	err  error
	note string
}

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
	issue   *issue
	project *project
	group   string // the heading it's listed under (its status); "" for none
	hue     string // the heading's colour: the status's own
}

// key names a row's issue or project, to find it again in a fresher list.
func (r row) key() string {
	if r.issue != nil {
		return r.issue.ID
	}
	return r.project.ID
}

type model struct {
	ctx      context.Context
	cfg      config // settled for the workspace in use (forWorkspace)
	baseCfg  config // as config.json has it
	client   source
	invoked  string // cwd of the space the picker was opened from
	repo     string // the repo it's in (repoKey), which remembers its workspace
	index    workspaceIndex
	ws       *workspace // the workspace shown; nil until chosen
	wsCursor int        // on the workspace screen; len(index.Workspaces) is "sign in"
	gen      int        // bumped on switching workspace (see issuesMsg)
	// draft is the new issue the popup was left on to attach files, to open
	// on again (attach.go).
	draft *draft
	// target is the issue the popup was opened on (HERDR_LINEAR_ISSUE), to
	// find once there's a workspace; direct, that its screen is where the
	// popup opened, so esc leaves; back, the popup it was opened from.
	target    *issueRef
	direct    bool
	back      *popupRef
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
	recent    []issue  // the Recent tab's issues, newest first
	drilled   *project // the project whose issues are listed, if any
	projIss   []issue
	loaded    map[tab]bool
	stale     map[tab]bool // it's the list from last time; the fresh one is on its way
	cache     bool         // lists are kept between popups (cache.go); not the demo's
	worktrees map[string]bool
	cursor    int
	offset    int
	// chose: you put the cursor where it is. Until then it stays on the top
	// row, whatever a fresher list puts there; after, it follows your pick.
	chose     bool
	lookup    []issue            // issues Linear found, for lookupFor
	lookupFor string             // the filter (lookupQuery) they were found for
	lookingUp string             // the one being looked up, if any
	cancel    context.CancelFunc // cancels an in-flight sign-in

	// An in-flight sign-in: the link to approve access on, whether a browser
	// was opened on it, and where an address pasted back goes.
	loginURL    string
	loginOpened bool
	paste       textinput.Model
	pasted      chan string

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

	// Quick add (create.go).
	form     issueForm
	created  *issue     // the issue just created, on its screen
	teamList []teamInfo // fetched once per workspace

	mouseX, mouseY int // last pointer position; -1 until the mouse moves
}

func newModel(ctx context.Context, cfg config, invoked string) model {
	ti := textinput.New()
	ti.Prompt = " › "
	ti.Placeholder = "Type to filter… ← → to change tabs"
	ti.PromptStyle, ti.PlaceholderStyle = styleDim, styleDim
	ti.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	return model{
		ctx: ctx, cfg: cfg, baseCfg: cfg, client: &linearClient{cfg: cfg}, invoked: invoked,
		mode: modeLoading, filter: ti, spin: sp, loaded: map[tab]bool{}, stale: map[tab]bool{}, worktrees: map[string]bool{},
		states: map[string][]workflowState{}, md: newMarkdown(cfg.Theme != "light"),
		mouseX: -1, mouseY: -1,
	}
}

func (m model) Init() tea.Cmd {
	if _, ok := m.client.(*demoSource); ok {
		return tea.Batch(m.spin.Tick, m.loadIssues(), m.loadProjects(), m.loadRecent(), m.loadWorktrees())
	}
	return tea.Batch(m.spin.Tick, m.loadWorkspaces())
}

func (m model) loadWorkspaces() tea.Cmd {
	ctx, cfg := m.ctx, m.baseCfg
	return func() tea.Msg {
		ix, err := loadWorkspaces(ctx, cfg)
		return workspacesMsg{ix, err}
	}
}

// useWorkspace shows w, forgetting what the last one loaded, and with
// remember saves it as this repo's workspace.
func (m model) useWorkspace(w workspace, remember bool) (model, tea.Cmd) {
	m.ws, m.gen = &w, m.gen+1
	m.cfg = m.baseCfg.forWorkspace(w.URLKey)
	m.client = &linearClient{cfg: m.cfg, ws: w}
	m.issues, m.projects, m.recent, m.drilled, m.projIss = nil, nil, nil, nil, nil
	m.lookup, m.lookupFor, m.lookingUp = nil, "", ""
	m.loaded, m.stale, m.states = map[tab]bool{}, map[tab]bool{}, map[string][]workflowState{}
	m.screen, m.cursor, m.offset, m.chose, m.err, m.flash = screenList, 0, 0, false, "", ""
	m.cur, m.curDetail, m.curProject, m.projDetail = nil, nil, nil, nil
	m.form, m.created, m.teamList = issueForm{}, nil, nil
	m.mode = modeLoading
	if m.cache {
		m = m.useCache(readListCache(w.ID))
	}
	// Every tab's list, so each says how many it has before you go to it.
	cmds := []tea.Cmd{m.spin.Tick, m.loadIssues(), m.loadProjects(), m.loadRecent(), m.loadWorktrees()}
	if m.target != nil {
		cmds = append(cmds, m.findTarget(*m.target))
	}
	if remember && m.repo != "" {
		ctx, repo := m.ctx, m.repo
		m.index.remember(repo, w.ID)
		cmds = append(cmds, func() tea.Msg {
			if err := updateIndex(ctx, func(ix *workspaceIndex) { ix.remember(repo, w.ID) }); err != nil {
				return noteMsg("Couldn't remember this repo's workspace, so you'll be asked again: " + err.Error())
			}
			return nil
		})
	}
	return m, tea.Batch(cmds...)
}

// useCache shows the lists the last popup loaded until fresh ones arrive.
func (m model) useCache(c cachedLists) model {
	if c.Mine != nil {
		m.issues, m.loaded[tabMine], m.stale[tabMine] = *c.Mine, true, true
	}
	if c.Projects != nil {
		m.projects, m.loaded[tabProjects], m.stale[tabProjects] = *c.Projects, true, true
	}
	if c.Recent != nil {
		m.recent, m.loaded[tabRecent], m.stale[tabRecent] = *c.Recent, true, true
	}
	if m.loaded[m.tab] {
		m.mode = modeList
		m.clampCursor()
	}
	return m
}

// keep saves tab t's fresh list for the next popup to open on.
func (m model) keep(t tab, issues []issue, projects []project, err error) tea.Cmd {
	if !m.cache || m.ws == nil || err != nil { // not one cut short, either
		return nil
	}
	id := m.ws.ID
	return func() tea.Msg { saveListCache(id, t, issues, projects); return nil }
}

// targetMsg is the issue the popup was opened on, found (or not).
type targetMsg struct {
	ref    issueRef
	issues []issue
	err    error
	gen    int
}

func (m model) findTarget(ref issueRef) tea.Cmd {
	client, ctx, gen := m.client, m.ctx, m.gen
	return func() tea.Msg {
		is, err := client.issuesByNumber(ctx, ref.Number, ref.Team)
		return targetMsg{ref, is, err, gen}
	}
}

// leave closes the popup (esc): for the one it was opened from, if it was.
// Going to a worktree, or ctrl+c, just closes it.
func (m model) leave() (tea.Model, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	if m.back != nil {
		_ = handOver(*m.back)
	}
	return m, tea.Quit
}

// escLabel is what esc is said to do where it closes the popup.
func (m model) escLabel() string {
	if m.back != nil {
		return "esc back"
	}
	return "esc close"
}

// chooseWorkspace opens the workspace screen, on the one in use.
func (m model) chooseWorkspace() model {
	m.mode, m.wsCursor = modeChooseWorkspace, 0
	for i, w := range m.index.Workspaces {
		if m.ws != nil && w.ID == m.ws.ID {
			m.wsCursor = i
		}
	}
	return m
}

func (m model) loadIssues() tea.Cmd {
	return func() tea.Msg {
		is, err := m.client.myIssues(m.ctx)
		return issuesMsg{is, err, m.gen}
	}
}

func (m model) loadRecent() tea.Cmd {
	return func() tea.Msg {
		is, err := m.client.createdIssues(m.ctx)
		return recentMsg{is, err, m.gen}
	}
}

func (m model) loadProjects() tea.Cmd {
	return func() tea.Msg {
		ps, err := m.client.projects(m.ctx)
		return projectsMsg{ps, err, m.gen}
	}
}

func (m model) loadProjectIssues(p project) tea.Cmd {
	return func() tea.Msg {
		is, err := m.client.projectIssues(m.ctx, p.ID)
		return projectIssuesMsg{p.ID, is, err, m.gen}
	}
}

// loadWorktrees collects the branches that already have a worktree, across the
// invoking repo and every configured one, to mark them in the list.
func (m model) loadWorktrees() tea.Cmd {
	if demo, ok := m.client.(*demoSource); ok {
		return func() tea.Msg { return worktreesMsg{demo.worktreeBranches(), m.gen} }
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
		return worktreesMsg{seen, m.gen}
	}
}

// ── update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, handled := m.updateDetail(msg); handled {
		return next, nil
	}
	if next, cmd, handled := m.updateCreate(msg); handled {
		return next, cmd
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

	case targetMsg:
		if msg.gen != m.gen || m.target == nil || *m.target != msg.ref {
			return m, nil
		}
		m.target = nil
		for _, is := range msg.issues {
			if strings.EqualFold(is.Identifier, msg.ref.String()) {
				m.direct = true
				return m.openIssue(is)
			}
		}
		if msg.err != nil {
			m.err = "Couldn't open " + msg.ref.String() + ": " + msg.err.Error()
		} else {
			m.err = msg.ref.String() + " isn't in this workspace"
		}
		return m, nil

	case issuesMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.stale[tabMine] = false
		if m.handleLoadErr(msg.err) {
			return m, nil
		}
		key := m.selectedKey()
		m.issues, m.loaded[tabMine] = msg.issues, true
		m.doneLoading(tabMine)
		m.reselect(key)
		return m, m.keep(tabMine, msg.issues, nil, msg.err)

	case recentMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.stale[tabRecent] = false
		if m.handleLoadErr(msg.err) {
			return m, nil
		}
		key := m.selectedKey()
		m.recent, m.loaded[tabRecent] = msg.issues, true
		m.doneLoading(tabRecent)
		m.reselect(key)
		return m, m.keep(tabRecent, msg.issues, nil, msg.err)

	case projectsMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.stale[tabProjects] = false
		if m.handleLoadErr(msg.err) {
			return m, nil
		}
		key := m.selectedKey()
		m.projects, m.loaded[tabProjects] = msg.projects, true
		m.doneLoading(tabProjects)
		m.reselect(key)
		return m, m.keep(tabProjects, nil, msg.projects, msg.err)

	case projectIssuesMsg:
		if msg.gen != m.gen || m.handleLoadErr(msg.err) {
			return m, nil
		}
		if m.drilled != nil && m.drilled.ID == msg.projectID {
			key := m.selectedKey()
			m.projIss = msg.issues
			if !m.modal() {
				m.mode = modeList
			}
			m.reselect(key)
		}
		return m, nil

	case lookupTickMsg:
		if msg.gen != m.gen || msg.query != m.lookingUp {
			return m, nil
		}
		if len(m.rows()) > 0 { // your list came in meanwhile, and has it
			m.lookingUp = ""
			return m, nil
		}
		client, ctx, gen, q, term := m.client, m.ctx, m.gen, msg.query, m.filter.Value()
		return m, func() tea.Msg {
			is, err := findIssues(ctx, client, term)
			return lookupMsg{q, is, err, gen}
		}

	case lookupMsg:
		if msg.gen != m.gen || msg.query != m.lookingUp {
			return m, nil
		}
		m.lookup, m.lookupFor, m.lookingUp = msg.issues, msg.query, ""
		if msg.err != nil {
			m.err = "Couldn't search Linear for " + msg.query + ": " + msg.err.Error()
		}
		m.clampCursor()
		return m, nil

	case worktreesMsg:
		if msg.gen == m.gen {
			m.worktrees = msg.branches
		}
		return m, nil

	case loginStatusMsg:
		m.status = string(msg)
		return m, nil

	case loginLinkMsg:
		m.loginURL, m.loginOpened = msg.url, msg.opened
		return m, nil

	case noteMsg:
		m.err = string(msg)
		return m, nil

	case workspacesMsg:
		m.index = msg.index
		note := ""
		if msg.err != nil && !errors.Is(msg.err, errSignedOut) {
			// Moving a 0.2 sign-in failed (offline, say). Without other
			// workspaces that's all there is to say; with them, they still work.
			if len(m.index.Workspaces) == 0 {
				m.mode, m.err = modeSignedOut, msg.err.Error()
				return m, nil
			}
			note = "Couldn't finish moving your earlier sign-in: " + msg.err.Error()
		}
		var cmd tea.Cmd
		pick := m.index.pick(m.repo)
		if m.target != nil {
			pick = workspaceFor(m.index, m.repo, *m.target)
		}
		if m.draft != nil && m.index.byID(m.draft.WorkspaceID) != nil {
			pick = m.index.byID(m.draft.WorkspaceID)
		}
		switch w := pick; {
		case len(m.index.Workspaces) == 0:
			m.mode = modeSignedOut
		case w != nil:
			m, cmd = m.useWorkspace(*w, false)
			// Back from attaching files: the new issue as it was.
			if d := m.draft; d != nil && d.WorkspaceID == w.ID {
				var more tea.Cmd
				m.draft = nil
				m, more = m.restoreDraft(*d)
				cmd = tea.Batch(cmd, more)
			}
		default:
			m = m.chooseWorkspace()
		}
		if note != "" {
			m.err = note
		}
		return m, cmd

	case loginDoneMsg:
		m.cancel, m.loginURL, m.pasted = nil, "", nil
		if msg.err != nil {
			m.err = msg.err.Error()
			m.mode = modeSignedOut
			if len(m.index.Workspaces) > 0 {
				m = m.chooseWorkspace()
			}
			return m, nil
		}
		m.status = ""
		m.index.add(msg.ws)
		return m.useWorkspace(msg.ws, len(m.index.Workspaces) > 1)

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
	if m.modal() {
		// A load that lands mid-action, mid-sign-in or on the workspace
		// screen only reports; it doesn't take you out of it.
		m.err = err.Error()
		return true
	}
	if errors.Is(err, errSignedOut) {
		m.mode, m.err = modeSignedOut, ""
	} else {
		m.mode, m.err = modeList, err.Error()
	}
	return true
}

// modal is a mode a load's reply must leave alone: an action under way (its
// own reply ends it), a sign-in, or the workspace screen.
func (m model) modal() bool {
	return m.mode == modeBusy || m.mode == modeSigningIn || m.mode == modeChooseWorkspace
}

// doneLoading shows the list once tab t's load lands, if it's the one being
// waited for: every tab loads when the popup opens, and another's landing
// first doesn't make this one ready. Not while something modal is on screen.
func (m *model) doneLoading(t tab) {
	if !m.modal() && (m.tab == t || m.loaded[m.tab]) && (m.drilled == nil || m.projIss != nil) {
		m.mode = modeList
	}
}

func (m model) reload() tea.Cmd {
	switch {
	case m.drilled != nil:
		return tea.Batch(m.spin.Tick, m.loadProjectIssues(*m.drilled))
	case m.tab == tabProjects:
		return tea.Batch(m.spin.Tick, m.loadProjects())
	case m.tab == tabRecent:
		return tea.Batch(m.spin.Tick, m.loadRecent(), m.loadWorktrees())
	default:
		return tea.Batch(m.spin.Tick, m.loadIssues(), m.loadWorktrees())
	}
}

func (m model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// In the list, ctrl+c is Linear's c: a new issue (letters type in the
	// filter there). Everywhere else it closes, as in any terminal app.
	if k.String() == "ctrl+c" && !(m.mode == modeList && m.screen == screenList) {
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
		case "ctrl+t":
			// Signed out of this workspace, say; the others may be fine.
			if len(m.index.Workspaces) > 0 {
				return m.chooseWorkspace(), nil
			}
		case "esc", "q":
			return m.leave()
		}
		return m, nil
	case modeSigningIn:
		return m.handleSignInKey(k)
	case modeChooseWorkspace:
		return m.handleWorkspaceKey(k)
	case modeBusy, modeLoading:
		if m.mode == modeLoading && m.screen != screenList {
			break // an issue opened straight away doesn't wait for the lists behind it
		}
		if k.String() == "esc" {
			return m.leave()
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
	if m.screen == screenCreate || m.screen == screenCreated {
		return m.handleCreateKey(k)
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
			m.lookup, m.lookupFor, m.lookingUp = nil, "", ""
			m.cursor, m.offset, m.chose = 0, 0, false
		case m.drilled != nil:
			m.drilled, m.projIss = nil, nil
			m.cursor, m.offset, m.chose = 0, 0, false
		default:
			return m.leave()
		}
		return m, nil
	case "ctrl+t":
		if _, demo := m.client.(*demoSource); !demo {
			return m.chooseWorkspace(), nil
		}
		return m, nil
	case "tab", "shift+tab":
		step := tab(1)
		if k.String() == "shift+tab" {
			step = tabCount - 1
		}
		return m.switchTab((m.tab + step) % tabCount)
	case "left", "right":
		// With text in the filter, arrows edit it; otherwise they move
		// between tabs, stopping at either end, and ← leaves a project's issues.
		if m.filter.Value() != "" {
			break
		}
		switch {
		case k.String() == "left" && m.drilled != nil:
			m.drilled, m.projIss = nil, nil
			m.cursor, m.offset, m.chose = 0, 0, false
			m.clampCursor()
		case k.String() == "right" && m.tab < tabCount-1:
			return m.switchTab(m.tab + 1)
		case k.String() == "left" && m.tab > 0:
			return m.switchTab(m.tab - 1)
		}
		return m, nil
	case "up", "ctrl+p", "ctrl+k":
		m.move(-1)
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		m.move(1)
		return m, nil
	case "pgup":
		m.move(-m.visibleRows())
		return m, nil
	case "pgdown":
		m.move(m.visibleRows())
		return m, nil
	case "ctrl+r":
		m.err = ""
		if m.drilled == nil && m.loaded[m.tab] {
			// What's listed stays, and stays usable, until the fresh one lands.
			m.stale[m.tab] = true
			return m, m.reload()
		}
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
				return m, m.openURL(u)
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
	case "ctrl+c":
		// In a project, or on one, the new issue goes in it.
		p := m.drilled
		if r := m.selected(); p == nil && r != nil && r.project != nil {
			p = r.project
		}
		return m.openCreate(p, nil)
	}

	// Anything else edits the filter.
	prev := m.filter.Value()
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(k)
	if m.filter.Value() != prev {
		m.cursor, m.offset, m.chose = 0, 0, false
		m.clampCursor()
		return m, tea.Batch(cmd, m.startLookup())
	}
	return m, cmd
}

// switchTab shows tab t's list, loading it the first time.
func (m model) switchTab(t tab) (tea.Model, tea.Cmd) {
	m.drilled, m.projIss, m.err = nil, nil, ""
	m.tab, m.cursor, m.offset, m.chose = t, 0, 0, false
	if !m.loaded[m.tab] {
		m.mode = modeLoading
		return m, m.reload()
	}
	m.mode = modeList // another tab may still be loading; this one isn't
	m.clampCursor()
	return m, nil
}

// issueNumber is an issue number as typed in the filter: 1038, or with its
// team, ENG-1038.
var issueNumber = regexp.MustCompile(`^(?:([A-Z][A-Z0-9_]*)-)?([0-9]{1,9})$`)

// splitIssueNumber splits a lookup query (lookupQuery) into team and number.
func splitIssueNumber(q string) (string, int) {
	sub := issueNumber.FindStringSubmatch(q)
	n, _ := strconv.Atoi(sub[2])
	return sub[1], n
}

// lookupQuery is the filter as a lookup, in a form that compares: whatever
// its case or the spaces around it, it's one lookup.
func (m model) lookupQuery() string {
	return strings.ToUpper(strings.TrimSpace(m.filter.Value()))
}

// lookupDelay lets typing settle, so 1038 is one request, not four. A search
// waits longer: words take longer to type, and searches are rationed.
const (
	lookupDelay = 300 * time.Millisecond
	searchDelay = 500 * time.Millisecond
)

// startLookup asks Linear about what the filter holds (findIssues), once your
// own list has nothing for it: it has only your open issues, and the one
// you're after may be someone else's, unassigned or closed.
func (m *model) startLookup() tea.Cmd {
	q := m.lookupQuery()
	if q == m.lookupFor || q == m.lookingUp {
		return nil
	}
	m.lookup, m.lookupFor, m.lookingUp = nil, "", ""
	if m.tab != tabMine || m.drilled != nil || len(m.rows()) > 0 {
		return nil
	}
	delay := lookupDelay
	if !issueNumber.MatchString(q) {
		if !searchable(q) {
			return nil
		}
		delay = searchDelay
	}
	m.lookingUp = q
	gen := m.gen
	return tea.Tick(delay, func(time.Time) tea.Msg { return lookupTickMsg{q, gen} })
}

func (m model) startLogin() (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel, m.mode, m.err, m.flash, m.status = cancel, modeSigningIn, "", "", "Starting sign-in…"
	m.loginURL, m.loginOpened = "", false
	m.paste = textinput.New()
	m.paste.Prompt = "› "
	m.paste.Placeholder = "paste the address here"
	m.paste.Focus()
	// Buffered, so a paste never waits on sign-in to take it; one at a time
	// is plenty for something typed by hand.
	m.pasted = make(chan string, 1)
	cfg, pasted := m.baseCfg, m.pasted
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		w, err := login(ctx, cfg, loginUI{
			status: func(s string) { program.Send(loginStatusMsg(s)) },
			link:   func(u string, opened bool) { program.Send(loginLinkMsg{u, opened}) },
			pasted: pasted,
		})
		return loginDoneMsg{w, err}
	})
}

// handleSignInKey: while signing in, what you type goes in the paste field;
// enter hands it to the sign-in, ctrl+y copies the link, esc cancels.
func (m model) handleSignInKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		if m.cancel != nil {
			m.cancel()
		}
		return m, nil
	case "ctrl+y":
		if m.loginURL == "" {
			return m, nil
		}
		if term, err := copyText(m.loginURL); err != nil {
			m.err = "Couldn't copy the link: " + err.Error()
		} else {
			m.err, m.flash = "", copied("the link", term)
		}
		return m, nil
	case "enter":
		v := strings.TrimSpace(m.paste.Value())
		if v == "" {
			return m, nil
		}
		select {
		case m.pasted <- v:
			m.paste.SetValue("")
			m.flash = ""
		default: // the last one isn't read yet
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.paste, cmd = m.paste.Update(k)
	return m, cmd
}

// handleWorkspaceKey is the workspace screen: pick one for this repo, or
// sign in to another.
func (m model) handleWorkspaceKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.index.Workspaces)
	switch k.String() {
	case "up", "ctrl+p", "ctrl+k":
		m.wsCursor = max(0, m.wsCursor-1)
	case "down", "ctrl+n", "ctrl+j":
		m.wsCursor = min(n, m.wsCursor+1)
	case "enter":
		if m.wsCursor == n {
			return m.startLogin()
		}
		return m.useWorkspace(m.index.Workspaces[m.wsCursor], true)
	case "esc", "ctrl+t":
		if m.ws == nil {
			return m.leave()
		}
		if !m.loaded[m.tab] {
			m.mode = modeLoading
			return m, m.reload()
		}
		m.mode = modeList
	case "q":
		if m.ws == nil {
			return m, tea.Quit
		}
	}
	return m, nil
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
			return m.runProjectStart(*r.project)
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

// runProjectStart is start on a project (doProjectStart).
func (m model) runProjectStart(p project) (tea.Model, tea.Cmd) {
	m.mode, m.err, m.status = modeBusy, "", "Starting "+shorten(p.Name, 40)+"…"
	cfg, client, ctx, invoked := m.cfg, m.client, m.ctx, m.invoked
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		if demo, ok := client.(*demoSource); ok {
			return demo.projectStart(p)
		}
		return doProjectStart(ctx, cfg, invoked, p)
	})
}

// ── rows + cursor ─────────────────────────────────────────────────────────────

func (m model) rows() []row {
	q := m.filter.Value()
	var rows []row
	addIssues := func(list []issue) {
		for i := range list {
			if is := &list[i]; is.matches(q) {
				// Both lists come sorted by status: each is a group.
				rows = append(rows, row{issue: is, group: is.State.Name, hue: is.State.Color})
			}
		}
	}
	switch {
	case m.drilled != nil:
		addIssues(m.projIss)
	case m.tab == tabRecent:
		// Newest first, as filed: no status groups to scatter them.
		for i := range m.recent {
			if is := &m.recent[i]; is.matches(q) {
				rows = append(rows, row{issue: is})
			}
		}
	case m.tab == tabMine:
		addIssues(m.issues)
		if m.lookupFor != "" && m.lookupFor == m.lookupQuery() {
			mine := map[string]bool{}
			for _, is := range m.issues {
				mine[is.ID] = true
			}
			for i := range m.lookup {
				if is := &m.lookup[i]; !mine[is.ID] {
					rows = append(rows, row{issue: is, group: "Not in your issues"})
				}
			}
		}
	default:
		for i := range m.projects {
			if p := &m.projects[i]; p.matches(q) {
				rows = append(rows, row{project: p, group: p.Status.Name, hue: p.Status.Color})
			}
		}
	}
	return rows
}

func (m model) selected() *row {
	rows := m.rows()
	if m.cursor >= 0 && m.cursor < len(rows) {
		return &rows[m.cursor]
	}
	return nil
}

// selectedKey is the row you put the cursor on, if you did (row.key).
func (m model) selectedKey() string {
	if r := m.selected(); r != nil && m.chose {
		return r.key()
	}
	return ""
}

// reselect puts the cursor back on the row it was on (selectedKey) once a
// fresher list has moved it; on the top row if you hadn't picked one, or
// it's gone.
func (m *model) reselect(key string) {
	if key == "" {
		if !m.chose {
			m.cursor, m.offset = 0, 0
		}
		m.clampCursor()
		return
	}
	for i, r := range m.rows() {
		if r.key() == key {
			m.cursor = i
			m.scrollTo()
			return
		}
	}
	m.cursor, m.offset, m.chose = 0, 0, false
	m.clampCursor()
}

func (m *model) move(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		return
	}
	m.cursor, m.chose = min(max(m.cursor+delta, 0), len(rows)-1), true
	m.scrollTo()
}

func (m *model) clampCursor() {
	m.cursor = max(0, min(m.cursor, len(m.rows())-1))
	m.scrollTo()
}

// headed is whether the i-th row has its group's heading above it: the
// first of each group, and whichever row the list is scrolled to, so the top
// of the list always says what it's showing.
func (m model) headed(rows []row, i int) bool {
	if rows[i].group == "" {
		return false
	}
	return i == 0 || i == m.offset || rows[i-1].group != rows[i].group
}

// headingHeight is the lines the i-th row's heading takes: the heading, and
// a blank above it unless it's the top of the list; or none.
func (m model) headingHeight(rows []row, i int) int {
	switch {
	case !m.headed(rows, i):
		return 0
	case i == m.offset:
		return 1
	}
	return 2
}

// viewHeading is a group's heading: its name in its status's colour, and
// how many are listed in it.
func (m model) viewHeading(rows []row, r row) string {
	n := 0
	for _, o := range rows {
		if o.group == r.group {
			n++
		}
	}
	name := styleHeader
	if r.hue != "" {
		name = lipgloss.NewStyle().Foreground(lipgloss.Color(r.hue)).Bold(true)
	}
	return " " + name.Render(shorten(r.group, max(1, m.width-8))) + styleDim.Render(" "+strconv.Itoa(n))
}

// scrollTo keeps the selected row on screen, heading and all.
func (m *model) scrollTo() {
	rows := m.rows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	for m.offset < m.cursor && m.cursor < len(rows) {
		used := 0
		for i := m.offset; i <= m.cursor; i++ {
			used += m.headingHeight(rows, i) + 1
		}
		if used <= m.listHeight() {
			break
		}
		m.offset++
	}
	m.offset = max(0, min(m.offset, len(rows)-1))
}

// visibleRows is how many rows fit from the offset: a page.
func (m model) visibleRows() int {
	rows := m.rows()
	n, used := 0, 0
	for i := m.offset; i < len(rows); i++ {
		used += m.headingHeight(rows, i) + 1
		if used > m.listHeight() {
			break
		}
		n++
	}
	return max(1, n)
}

// rowAt is the row drawn on screen line y, if any: a heading isn't one.
func (m model) rowAt(y int) (int, bool) {
	if m.screen != screenList {
		return 0, false
	}
	rows := m.rows()
	top, end := listTop, listTop+m.listHeight()
	for i := m.offset; i < len(rows); i++ {
		if top += m.headingHeight(rows, i); top >= end {
			break
		}
		if y == top {
			return i, true
		}
		top++
	}
	return 0, false
}

func (m model) listHeight() int {
	// tabs + blank + filter + blank above; status line + footer below, the
	// footer on the popup's last line (mouse.go counts on that).
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
		switch {
		case m.ws != nil:
			b.WriteString("\n  You're signed out of " + styleHeader.Render(m.ws.Name) + ".\n\n  Press enter to sign in again with your browser")
			if len(m.index.Workspaces) > 1 {
				b.WriteString(", or ctrl+t for another workspace")
			}
			b.WriteString(".\n")
		default:
			b.WriteString("\n  Linear isn't connected yet.\n\n  Press enter to sign in with your browser.\n")
		}
		if m.err != "" {
			b.WriteString("\n  " + styleErr.Render(m.err) + "\n")
		}
		return b.String()
	case modeSigningIn:
		b.WriteString(m.viewSigningIn())
		return b.String()
	case modeChooseWorkspace:
		b.WriteString(m.viewWorkspaces())
		return b.String()
	}

	if m.screen != screenList {
		b.WriteString("\n")
		switch m.screen {
		case screenProject:
			b.WriteString(m.viewProject())
		case screenCreate:
			b.WriteString(m.viewCreate())
		case screenCreated:
			b.WriteString(m.viewCreated())
		default:
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

	b.WriteString("\n" + m.filter.View() + "\n\n")
	rows := m.rows()
	h, used := m.listHeight(), 0
	for i := m.offset; i < len(rows); i++ {
		// A heading with no room for a row under it says nothing.
		if used+m.headingHeight(rows, i) >= h {
			break
		}
		if m.headed(rows, i) {
			if i != m.offset {
				b.WriteString("\n")
			}
			b.WriteString(m.viewHeading(rows, rows[i]) + "\n")
		}
		b.WriteString(m.viewRow(rows[i], i == m.cursor) + "\n")
		used += m.headingHeight(rows, i) + 1
	}
	if len(rows) == 0 {
		switch {
		case m.mode == modeLoading:
			b.WriteString("  " + m.spin.View() + " Loading…\n")
		case m.lookingUp != "":
			b.WriteString(styleDim.Render("  Searching Linear for "+strings.TrimSpace(m.filter.Value())+"…") + "\n")
		default:
			b.WriteString(styleDim.Render("  "+m.emptyText()) + "\n")
		}
		used++
	}
	for ; used < h; used++ {
		b.WriteString("\n")
	}

	b.WriteString(m.statusLine())
	b.WriteString(m.renderFooter(m.footer()))
	return b.String()
}

// emptyText says why a list has nothing in it.
func (m model) emptyText() string {
	switch {
	case m.filter.Value() != "":
		return "Nothing matches."
	case m.drilled != nil:
		return "No open issues in this project. ctrl+c adds one."
	case m.tab == tabProjects:
		return "No projects under way."
	case m.tab == tabRecent:
		return "You haven't created an issue lately. ctrl+c adds one."
	}
	return "No open issues assigned to you."
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
	case m.screen == screenList && m.mode == modeList:
		return m.about() + "\n"
	}
	return "\n"
}

// about is what a row has no room for, of the selected one, for the status
// line: an issue's priority, with its branch on the right; a project's
// target date. Selecting a row doesn't change its height.
func (m model) about() string {
	r := m.selected()
	if r == nil {
		return ""
	}
	left, branch := "", ""
	if is := r.issue; is != nil {
		branch = issueTarget(*is).Branch
		if is.Priority > 1 && is.Priority < len(priorityNames) {
			left = priorityNames[is.Priority] + " priority"
		}
	} else {
		branch = projectTarget(*r.project).Branch
		if r.project.TargetDate != "" {
			left = "Target " + r.project.TargetDate
		}
	}
	right := styleBranch.Render(shorten(branch, max(1, min(48, m.width/2))))
	left = styleDim.Render(shorten(left, max(1, m.width-lipgloss.Width(right)-4)))
	return " " + left + strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2)) + right
}

// viewSigningIn keeps the link on screen for the whole sign-in: opened in a
// browser or not, you may need it (a browser elsewhere, or none at all).
func (m model) viewSigningIn() string {
	var b strings.Builder
	w := max(20, m.width-4)
	b.WriteString("\n  " + m.spin.View() + " " + wrapText(clean(m.status, false), w, "\n    ") + "\n")
	if m.loginURL == "" {
		return b.String()
	}
	if m.loginOpened {
		b.WriteString("\n  If your browser didn't open, open this link:\n\n")
	} else {
		b.WriteString("\n  Open this link in a browser, on any computer:\n\n")
	}
	// Broken by hand, the terminal would cut it off at the edge. Copy it
	// with ctrl+y rather than selecting it: the lines would come with it.
	// A line at a time: styled as a block, the lines would be padded with
	// spaces, which a selection would pick up.
	for _, line := range strings.Split(wrapText(m.loginURL, w, "\n"), "\n") {
		b.WriteString("  " + styleTree.Render(line) + "\n")
	}
	// Right under it and bright: the popup holds the mouse, so selecting
	// the link isn't how to copy it.
	b.WriteString("\n  " + styleHeader.Render("Press ctrl+y to copy the link.") + "\n\n")
	b.WriteString("  " + wrapText(pasteHint, w, "\n  ") + "\n\n")
	b.WriteString("  " + m.paste.View() + "\n\n")
	switch {
	case m.err != "":
		b.WriteString(" " + styleErr.Render(shorten(clean(m.err, false), max(10, m.width-2))) + "\n")
	case m.flash != "":
		b.WriteString(" " + styleOK.Render("✓ ") + shorten(m.flash, max(10, m.width-4)) + "\n")
	default:
		b.WriteString("\n")
	}
	b.WriteString(m.renderFooter([]hint{{"enter submit", "enter", hintAct}, {"^y copy link", "ctrl+y", hintView}, {"esc cancel", "esc", hintQuiet}}))
	return b.String()
}

// wrapText breaks s into lines of at most w cells, between words where it
// can and anywhere where it can't (a URL), joined by sep.
func wrapText(s string, w int, sep string) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		for ansi.StringWidth(word) > w {
			if line != "" {
				lines, line = append(lines, line), ""
			}
			head := ansi.Truncate(word, w, "")
			lines, word = append(lines, head), word[len(head):]
		}
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= w:
			line += " " + word
		default:
			lines, line = append(lines, line), word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, sep)
}

func (m model) viewWorkspaces() string {
	var b strings.Builder
	if m.ws == nil && m.repo != "" {
		b.WriteString("\n  Which Linear workspace is " + styleHeader.Render(shorten(filepath.Base(m.repo), 40)) + " in?\n\n")
	} else {
		b.WriteString("\n  Linear workspace for this repo:\n\n")
	}
	line := func(i int, text string) {
		l := "   " + text
		if i == m.wsCursor {
			l = highlight(" › "+text, max(10, m.width))
		}
		b.WriteString(l + "\n")
	}
	for i, w := range m.index.Workspaces {
		line(i, shorten(w.Name, max(10, m.width/2))+styleDim.Render("  "+shorten(w.URLKey, 30)))
	}
	line(len(m.index.Workspaces), styleDim.Render("+ Sign in to another workspace"))
	b.WriteString("\n  " + styleDim.Render("enter choose · esc back") + "\n")
	if m.err != "" {
		b.WriteString("\n  " + styleErr.Render(shorten(clean(m.err, false), max(10, m.width-4))) + "\n")
	}
	return b.String()
}

// tabCell is one tab along the top, and the cells it's drawn in (x0 up to
// x1), for the mouse.
type tabCell struct {
	tab    tab
	x0, x1 int
}

// tabName is a tab's name; short, for a popup too narrow for it in full.
func tabName(t tab, short bool) string {
	switch {
	case t == tabProjects:
		return "Projects"
	case t == tabRecent:
		return "Recent"
	case short:
		return "Mine"
	}
	return "My issues"
}

// tabSize is how many tab t lists, once it has loaded.
func (m model) tabSize(t tab) (int, bool) {
	switch t {
	case tabProjects:
		return len(m.projects), m.loaded[t]
	case tabRecent:
		return len(m.recent), m.loaded[t]
	}
	return len(m.issues), m.loaded[t]
}

// tabBar is the popup's first line: the tabs, each with how many it lists,
// the one shown as a pill, in line with the keys' pills along the bottom.
// The popup's name is on its frame, which herdr draws. Too wide for the
// popup, the labels shorten. On an issue's or a project's screen no tab is
// the one shown: they're the way back.
func (m model) tabBar() (string, []tabCell) {
	cells := []tabCell{{tab: tabMine}, {tab: tabProjects}, {tab: tabRecent}}
	var line string
	for _, short := range []bool{false, true} {
		line = " "
		x := lipgloss.Width(line)
		for i, c := range cells {
			label, n := tabName(c.tab, short), ""
			if size, ok := m.tabSize(c.tab); ok {
				n = " " + strconv.Itoa(size)
			}
			cell := " " + styleText.Render(label) + styleDim.Render(n) + " "
			if c.tab == m.tab && m.screen == screenList {
				cell = pill(look.Accent, label+n)
			}
			line += cell
			cells[i].x0, cells[i].x1 = x, x+lipgloss.Width(cell)
			x = cells[i].x1
		}
		if x <= m.width {
			break
		}
	}
	return line, cells
}

// viewTabs is the tab bar, with where you are past the lists, and on the
// right what's going on behind it and the workspace, when there's more than
// one to be in.
func (m model) viewTabs() string {
	line, _ := m.tabBar()
	switch {
	case m.screen == screenCreate:
		line += styleDim.Render(" › ") + styleHeader.Render("New issue")
	case m.screen == screenCreated:
		line += styleDim.Render(" › ") + styleHeader.Render(m.created.Identifier)
	case m.screen == screenProject:
		line += styleDim.Render(" › ") + styleHeader.Render(shorten(m.curProject.Name, 40))
	case m.screen != screenList:
		if m.drilled != nil {
			line += styleDim.Render(" › " + shorten(m.drilled.Name, 30))
		}
		line += styleDim.Render(" › ") + styleHeader.Render(m.cur.Identifier)
	case m.drilled != nil:
		line += styleDim.Render(" › ") + styleHeader.Render(shorten(m.drilled.Name, 40))
	}
	tag := ""
	if m.ws != nil && len(m.index.Workspaces) > 1 {
		tag = styleDim.Render(shorten(m.ws.Name, 24) + " ")
	} else if _, demo := m.client.(*demoSource); demo {
		tag = styleDim.Render("demo ")
	}
	// What's listed is from last time, or being refreshed.
	for t := range m.stale {
		if m.stale[t] && m.mode != modeBusy {
			tag = m.spin.View() + styleDim.Render(" updating ") + tag
			break
		}
	}
	if pad := m.width - lipgloss.Width(line) - lipgloss.Width(tag); tag != "" && pad > 1 {
		line += strings.Repeat(" ", pad) + tag
	}
	return ansi.Truncate(line, m.width, "") // narrower than the tabs at their shortest
}

// viewRow is a row's one line: there are too many issues for two. Its mark
// and identifier wear its status's colour (they read as one, as a PR's
// number does), then its title, and at the edge what there is to say of it,
// in words.
func (m model) viewRow(r row, selected bool) string {
	var line string
	if is := r.issue; is != nil {
		left := " " + colored(stateIcon(is.State), is.State.Color) + " " + colored(is.Identifier, is.State.Color) +
			strings.Repeat(" ", max(0, m.idWidth()-len(is.Identifier))) + " "
		line = fitRow(left, is.Title, m.issueMeta(is), m.width)
	} else {
		p := r.project
		line = fitRow(" "+colored(projectIcon(p.Status.Type), p.Status.Color)+" ", p.Name, m.projectMeta(p), m.width)
	}
	if selected {
		return highlight(line, m.width)
	}
	return line
}

// idWidth is the width of the longest identifier listed, so titles line up.
func (m model) idWidth() int {
	n := 1
	for _, r := range m.rows() {
		if r.issue != nil {
			n = max(n, len(r.issue.Identifier))
		}
	}
	return n
}

// meta is one thing said at a row's right edge. In a popup too narrow for
// them all, the ones with the lowest keep go first.
type meta struct {
	text string
	keep int
}

// issueMeta is what's right of an issue's title: that it's urgent, whose it
// is and its project (each only where the list doesn't already say), and
// that it has a worktree.
func (m model) issueMeta(is *issue) []meta {
	var ms []meta
	if is.Priority == 1 {
		ms = append(ms, meta{styleUrgent.Render("urgent"), 4})
	}
	if (m.drilled != nil || m.tab == tabRecent) && is.Assignee != nil && !is.Assignee.IsMe {
		ms = append(ms, meta{styleDim.Render(shorten(is.Assignee.Name, 20)), 2})
	}
	if m.drilled == nil && is.Project != nil {
		ms = append(ms, meta{styleDim.Render(shorten(is.Project.Name, 30)), 1})
	}
	if m.worktrees[issueTarget(*is).Branch] {
		ms = append(ms, meta{styleTree.Render("worktree"), 3})
	}
	return ms
}

// projectMeta is what's right of a project's name: that you lead it, that
// it has a worktree, and last, at the edge, how far along it is (its
// status is its heading).
func (m model) projectMeta(p *project) []meta {
	var ms []meta
	if p.Lead != nil && p.Lead.IsMe {
		ms = append(ms, meta{styleDim.Render("you lead"), 1})
	}
	if m.worktrees[projectTarget(*p).Branch] {
		ms = append(ms, meta{styleTree.Render("worktree"), 2})
	}
	return append(ms, meta{styleDim.Render(fmt.Sprintf("%d%%", int(p.Progress*100+0.5))), 3})
}

// minTitle is the room a title is given before the edge gives up a piece.
const minTitle = 24

// fitRow lays out left + title + right-aligned meta within w cells, a cell
// short of the edge. Where space runs out the meta goes first, a piece at a
// time, then the title is cut short; never the identifier.
func fitRow(left, title string, ms []meta, w int) string {
	lw := lipgloss.Width(left)
	join := func() string {
		parts := make([]string, len(ms))
		for i, m := range ms {
			parts[i] = m.text
		}
		return strings.Join(parts, styleDim.Render(" · "))
	}
	right := join()
	for len(ms) > 0 && w-lw-lipgloss.Width(right)-2 < min(lipgloss.Width(title), minTitle) {
		drop := 0
		for i, m := range ms {
			if m.keep < ms[drop].keep {
				drop = i
			}
		}
		ms = append(ms[:drop:drop], ms[drop+1:]...)
		right = join()
	}
	rw := lipgloss.Width(right)
	room := w - lw - rw - 2
	if rw == 0 {
		room = w - lw - 1
	}
	title = shorten(title, max(1, room))
	pad := max(1, w-lw-lipgloss.Width(title)-rw-1)
	return left + styleLead.Render(title) + strings.Repeat(" ", pad) + right
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
	// By kind, so the colours sit together: go, act, view, leave. The tabs'
	// keys are in the filter's placeholder.
	hs := []hint{{"enter details", "enter", hintGo}, {"^s start", "ctrl+s", hintAct}}
	if m.tab == tabProjects && m.drilled == nil {
		hs = append(hs, hint{"^w worktree", "ctrl+w", hintAct})
	}
	hs = append(hs, hint{"^c new issue", "ctrl+c", hintAct}, hint{"^o open in Linear", "ctrl+o", hintView}, hint{"^r refresh", "ctrl+r", hintView})
	if len(m.index.Workspaces) > 1 {
		hs = append(hs, hint{"^t workspace", "ctrl+t", hintView})
	}
	// esc says what it'll do.
	esc := m.escLabel()
	switch {
	case m.filter.Value() != "":
		esc = "esc clear"
	case m.drilled != nil:
		esc = "esc back"
	}
	return append(hs, hint{esc, "esc", hintQuiet})
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
	darkTerminal = cfg.Theme == "dark"
	useTheme(pickerTheme(darkTerminal))
	m := newModel(ctx, cfg, invoked)
	if demo {
		m.client = newDemoSource()
	} else {
		m.repo, m.cache = repoKey(invoked), true
		m.back = cameFrom()
		if ref, ok := targetIssue(); ok {
			m.target = &ref
		}
		if os.Getenv(draftEnv) == "1" {
			if d, err := takeDraft(os.Getenv(pickedEnv)); err == nil {
				m.draft = d
			}
		}
	}
	if !demo {
		defer notePopup()()
	}
	// WithContext: a SIGTERM (a newer open replacing this popup) ends it
	// cleanly, restoring the terminal.
	program = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithContext(ctx))
	_, err := program.Run()
	if errors.Is(err, context.Canceled) {
		return nil // replaced by a newer open, or interrupted: not an error
	}
	return err
}
