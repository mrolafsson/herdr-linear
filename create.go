package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Quick add: ctrl+c on the list (c on a project's screen, as in Linear) opens a form for a
// new issue. Opened on a project, the project and its team are filled in.
// Once it's created you can look at it, start it, or add another.

type teamInfo struct {
	ID           string `json:"id"`
	Key          string `json:"key"`
	Name         string `json:"name"`
	DefaultState *struct {
		ID string `json:"id"`
	} `json:"defaultIssueState"`
}

// newIssue is what the form sends to Linear.
type newIssue struct {
	TeamID, Title, Description string
	ProjectID                  string // "" = none
	StateID                    string // "" = Linear's choice (statuses not loaded yet)
	Priority                   int
	AssignToMe                 bool
}

type teamsMsg struct {
	teams []teamInfo
	err   error
	gen   int
}
type createdMsg struct {
	issue issue
	err   error
	gen   int
}

const (
	fieldTitle = iota
	fieldDesc
	fieldTeam
	fieldProject
	fieldStatus
	fieldPriority
	fieldAssignee
	fieldCount
)

var fieldNames = [fieldCount]string{"Title", "Details", "Team", "Project", "Status", "Priority", "Assignee"}

var priorityNames = []string{"No priority", "Urgent", "High", "Medium", "Low"}

type issueForm struct {
	from      screen // where esc goes back to
	focus     int
	title     textinput.Model
	desc      textarea.Model
	teamID    string
	projectID string
	stateID   string // "" = the team's default
	priority  int
	assignMe  bool

	// The list of choices open over a field: enter on it, or start typing.
	picking    bool
	pick       textinput.Model
	pickCursor int
}

// choice is one value a field can take.
type choice struct {
	id, label, glyph string
}

// ── opening ───────────────────────────────────────────────────────────────────

// openCreate opens the form, on p's project and team when p isn't nil. keep
// carries the last form's choices over, for "another".
func (m model) openCreate(p *project, keep *issueForm) (tea.Model, tea.Cmd) {
	f := issueForm{from: m.screen, assignMe: true}
	if f.from != screenProject {
		f.from = screenList
	}
	f.title = textinput.New()
	f.title.Prompt, f.title.Placeholder, f.title.CharLimit = "", "What needs doing?", 255
	f.desc = textarea.New()
	f.desc.Prompt, f.desc.Placeholder, f.desc.ShowLineNumbers = "", "optional, Markdown", false
	f.desc.FocusedStyle.CursorLine = lipgloss.NewStyle()
	f.desc.FocusedStyle.Placeholder = styleDim
	f.desc.BlurredStyle.Placeholder = styleDim
	f.desc.SetHeight(4)
	f.desc.SetWidth(m.formWidth())
	if keep != nil {
		f.from, f.teamID, f.projectID, f.stateID = keep.from, keep.teamID, keep.projectID, keep.stateID
		f.priority, f.assignMe = keep.priority, keep.assignMe
	}
	if p != nil {
		f.projectID = p.ID
		if len(p.Teams.Nodes) > 0 {
			f.teamID = p.Teams.Nodes[0].ID
		}
	}
	f.setFocus(fieldTitle)
	m.form, m.screen, m.created, m.err, m.flash = f, screenCreate, nil, "", ""
	if m.form.teamID == "" {
		m.form.teamID = m.defaultTeam()
	}
	cmds := []tea.Cmd{m.loadFormStates()}
	if m.teamList == nil {
		client, ctx, gen := m.client, m.ctx, m.gen
		cmds = append(cmds, func() tea.Msg {
			ts, err := client.teams(ctx)
			return teamsMsg{ts, err, gen}
		})
	}
	if !m.loaded[tabProjects] {
		cmds = append(cmds, m.loadProjects())
	}
	return m, tea.Batch(cmds...)
}

// defaultTeam is the team most of your issues are in, else the first.
func (m model) defaultTeam() string {
	n, best := map[string]int{}, ""
	for _, is := range m.issues {
		if n[is.Team.ID]++; best == "" || n[is.Team.ID] > n[best] {
			best = is.Team.ID
		}
	}
	for _, t := range m.teamList {
		if t.ID == best {
			return best
		}
	}
	if len(m.teamList) > 0 {
		return m.teamList[0].ID
	}
	return ""
}

// loadFormStates fetches the form's team's statuses, unless they're here.
func (m model) loadFormStates() tea.Cmd {
	teamID := m.form.teamID
	if teamID == "" || len(m.states[teamID]) > 0 {
		return nil
	}
	client, ctx, gen := m.client, m.ctx, m.gen
	return func() tea.Msg {
		s, err := client.teamStates(ctx, teamID)
		return statesMsg{teamID, s, err, gen}
	}
}

func (f *issueForm) setFocus(i int) {
	f.focus = i
	f.title.Blur()
	f.desc.Blur()
	switch i {
	case fieldTitle:
		f.title.Focus()
	case fieldDesc:
		f.desc.Focus()
	}
}

// ── messages ──────────────────────────────────────────────────────────────────

func (m model) updateCreate(msg tea.Msg) (model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case teamsMsg:
		if msg.gen != m.gen || m.handleLoadErr(msg.err) {
			return m, nil, true
		}
		m.teamList = msg.teams
		if m.screen == screenCreate && m.form.teamID == "" {
			m.form.teamID = m.defaultTeam()
			return m, m.loadFormStates(), true
		}
		return m, nil, true
	case createdMsg:
		if msg.gen != m.gen {
			return m, nil, true
		}
		m.mode = modeList
		if m.handleLoadErr(msg.err) {
			return m, nil, true
		}
		is := msg.issue
		m.addCreated(is)
		m.screen, m.created = screenCreated, &is
		return m, nil, true
	}
	return m, nil, false
}

// addCreated lists the new issue where it belongs, without a refetch.
func (m *model) addCreated(is issue) {
	if m.loaded[tabRecent] {
		m.recent = append([]issue{is}, m.recent...)
	}
	if !isOpenState(is.State.Type) {
		m.clampCursor()
		return
	}
	if is.Assignee != nil && is.Assignee.IsMe {
		m.issues = append(m.issues, is)
		sortIssues(m.issues)
	}
	if m.drilled != nil && is.Project != nil && is.Project.ID == m.drilled.ID {
		m.projIss = append(m.projIss, is)
		sortIssues(m.projIss)
	}
	m.clampCursor()
}

// ── choices ───────────────────────────────────────────────────────────────────

func (m model) team(id string) *teamInfo {
	for i := range m.teamList {
		if m.teamList[i].ID == id {
			return &m.teamList[i]
		}
	}
	return nil
}

func (m model) projectByID(id string) *project {
	for i := range m.projects {
		if m.projects[i].ID == id {
			return &m.projects[i]
		}
	}
	return nil
}

// formState is the status the issue will get: the chosen one, or the team's
// default.
func (m model) formState() *workflowState {
	id := m.form.stateID
	if id == "" {
		if t := m.team(m.form.teamID); t != nil && t.DefaultState != nil {
			id = t.DefaultState.ID
		}
	}
	for _, s := range m.states[m.form.teamID] {
		if s.ID == id {
			return &s
		}
	}
	return nil
}

func (m model) choices(field int) []choice {
	var cs []choice
	switch field {
	case fieldTeam:
		for _, t := range m.teamList {
			cs = append(cs, choice{t.ID, t.Name, styleDim.Render(t.Key)})
		}
	case fieldProject:
		cs = append(cs, choice{"", "No project", styleDim.Render("·")})
		for _, p := range m.projects {
			cs = append(cs, choice{p.ID, p.Name, colored(projectIcon(p.Status.Type), p.Status.Color)})
		}
	case fieldStatus:
		for _, s := range m.states[m.form.teamID] {
			if isOpenState(s.Type) {
				cs = append(cs, choice{s.ID, s.Name, colored(stateIcon(s), s.Color)})
			}
		}
	case fieldPriority:
		for i, p := range priorityNames {
			g := styleDim.Render("·")
			if i == 1 {
				g = styleUrgent.Render("!")
			}
			cs = append(cs, choice{string(rune('0' + i)), p, g})
		}
	case fieldAssignee:
		cs = []choice{{"me", "You", styleDim.Render("◆")}, {"", "Nobody", styleDim.Render("·")}}
	}
	return cs
}

// chosen is the id the field holds now.
func (m model) chosen(field int) string {
	f := m.form
	switch field {
	case fieldTeam:
		return f.teamID
	case fieldProject:
		return f.projectID
	case fieldStatus:
		if s := m.formState(); s != nil {
			return s.ID
		}
	case fieldPriority:
		return string(rune('0' + f.priority))
	case fieldAssignee:
		if f.assignMe {
			return "me"
		}
	}
	return ""
}

// choose sets a field. A project from another team brings its team along; a
// team the project isn't in drops the project. A new team resets the status.
func (m *model) choose(field int, id string) tea.Cmd {
	f := &m.form
	setTeam := func(t string) {
		if t != f.teamID {
			f.teamID, f.stateID = t, ""
		}
	}
	switch field {
	case fieldTeam:
		setTeam(id)
		if p := m.projectByID(f.projectID); p != nil && !inTeam(*p, id) {
			f.projectID = ""
		}
	case fieldProject:
		f.projectID = id
		if p := m.projectByID(id); p != nil && !inTeam(*p, f.teamID) && len(p.Teams.Nodes) > 0 {
			setTeam(p.Teams.Nodes[0].ID)
		}
	case fieldStatus:
		f.stateID = id
	case fieldPriority:
		f.priority = int(id[0] - '0')
	case fieldAssignee:
		f.assignMe = id == "me"
	}
	return m.loadFormStates()
}

func inTeam(p project, teamID string) bool {
	for _, t := range p.Teams.Nodes {
		if t.ID == teamID {
			return true
		}
	}
	return false
}

// cycle steps a field to its next or previous choice.
func (m *model) cycle(field, delta int) tea.Cmd {
	cs := m.choices(field)
	if len(cs) == 0 {
		return nil
	}
	i := 0
	for j, c := range cs {
		if c.id == m.chosen(field) {
			i = j
		}
	}
	return m.choose(field, cs[(i+delta+len(cs))%len(cs)].id)
}

// picked is the open list's choices, as filtered.
func (m model) picked() []choice {
	q := strings.Fields(strings.ToLower(m.form.pick.Value()))
	var out []choice
	for _, c := range m.choices(m.form.focus) {
		hay := strings.ToLower(c.label + " " + c.glyph)
		ok := true
		for _, w := range q {
			ok = ok && strings.Contains(hay, w)
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

func (m *model) openPick(typed string) {
	f := &m.form
	f.picking, f.pickCursor = true, 0
	f.pick = textinput.New()
	f.pick.Prompt, f.pick.Placeholder = "› ", "filter"
	f.pick.Focus()
	f.pick.SetValue(typed)
	if typed == "" {
		for i, c := range m.picked() {
			if c.id == m.chosen(f.focus) {
				f.pickCursor = i
			}
		}
	}
}

// ── keys ──────────────────────────────────────────────────────────────────────

func (m model) handleCreateKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.screen == screenCreated {
		return m.handleCreatedKey(k)
	}
	f := &m.form
	m.flash = ""
	if f.picking {
		cs := m.picked()
		switch k.String() {
		case "esc":
			f.picking = false
		case "up", "ctrl+p", "ctrl+k":
			f.pickCursor = max(0, f.pickCursor-1)
		case "down", "ctrl+n", "ctrl+j":
			f.pickCursor = max(0, min(len(cs)-1, f.pickCursor+1))
		case "enter":
			if f.pickCursor < len(cs) {
				f.picking = false
				return m, m.choose(f.focus, cs[f.pickCursor].id)
			}
		default:
			prev := f.pick.Value()
			var cmd tea.Cmd
			f.pick, cmd = f.pick.Update(k)
			if f.pick.Value() != prev {
				f.pickCursor = 0
			}
			return m, cmd
		}
		return m, nil
	}

	choiceField := f.focus >= fieldTeam
	switch k.String() {
	case "esc":
		m.screen, m.err = f.from, ""
		return m, nil
	case "ctrl+s":
		return m.submit()
	case "tab":
		f.setFocus((f.focus + 1) % fieldCount)
		return m, nil
	case "shift+tab":
		f.setFocus((f.focus + fieldCount - 1) % fieldCount)
		return m, nil
	case "up", "down":
		// In the details, the arrows move through its lines first.
		up := k.String() == "up"
		if f.focus == fieldDesc && (up && f.desc.Line() > 0 || !up && f.desc.Line() < f.desc.LineCount()-1) {
			break
		}
		if up && f.focus > 0 {
			f.setFocus(f.focus - 1)
		} else if !up && f.focus < fieldCount-1 {
			f.setFocus(f.focus + 1)
		}
		return m, nil
	case "enter":
		switch {
		case f.focus == fieldTitle:
			return m.submit()
		case choiceField:
			m.openPick("")
			return m, nil
		}
	case "left", "right":
		if choiceField {
			delta := 1
			if k.String() == "left" {
				delta = -1
			}
			return m, m.cycle(f.focus, delta)
		}
	}

	var cmd tea.Cmd
	switch {
	case f.focus == fieldTitle:
		f.title, cmd = f.title.Update(k)
	case f.focus == fieldDesc:
		// The textarea wraps and scrolls by its width as it takes keys, so it
		// has to be the width it's drawn at, or what's typed scrolls out of view.
		f.desc.SetWidth(m.formWidth())
		f.desc, cmd = f.desc.Update(k)
	case k.Type == tea.KeyRunes:
		// Typing on a choice opens its list, filtered by what was typed.
		m.openPick(string(k.Runes))
	}
	return m, cmd
}

func (m model) submit() (tea.Model, tea.Cmd) {
	f := m.form
	in := newIssue{
		TeamID: f.teamID, Title: strings.TrimSpace(f.title.Value()), Description: strings.TrimSpace(f.desc.Value()),
		ProjectID: f.projectID, StateID: f.stateID, Priority: f.priority, AssignToMe: f.assignMe,
	}
	// The status shown is the one sent, the team's default included: with
	// none given, Linear puts an issue made through its API in Triage on a
	// team that has it on, not where the form said it would go.
	if s := m.formState(); s != nil {
		in.StateID = s.ID
	}
	switch {
	case in.Title == "":
		m.err = "Give it a title first"
		m.form.setFocus(fieldTitle)
		return m, nil
	case in.TeamID == "" && m.teamList == nil:
		m.err = "Still loading your teams; try again in a moment"
		return m, nil
	case in.TeamID == "":
		m.err = "Choose a team first"
		m.form.setFocus(fieldTeam)
		return m, nil
	}
	m.mode, m.err, m.status = modeBusy, "", "Creating "+shorten(in.Title, 40)+"…"
	client, ctx, gen := m.client, m.ctx, m.gen
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		is, err := client.createIssue(ctx, in)
		return createdMsg{is, err, gen}
	})
}

// handleCreatedKey is the screen after creating: view it, start it, or add
// another.
func (m model) handleCreatedKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	is := *m.created
	switch k.String() {
	case "enter", "v":
		return m.openIssue(is)
	case "s":
		return m.runWorktree(issueTarget(is), &is, true)
	case "c", "n":
		keep := m.form
		return m.openCreate(nil, &keep)
	case "o":
		return m, m.openURL(is.URL)
	case "esc", "q":
		m.screen, m.created, m.err, m.flash = m.form.from, nil, "", ""
	}
	return m, nil
}

// ── views ─────────────────────────────────────────────────────────────────────

const formLabel = 10 // the field names' column

// formWidth is the room the title and details have, right of the labels.
func (m model) formWidth() int { return max(20, m.width-formLabel-3) }

func (m model) viewCreate() string {
	f := m.form
	w := m.formWidth()
	var b strings.Builder
	b.WriteString(styleHeader.Render(" New issue") + "\n\n")
	for i := 0; i < fieldCount; i++ {
		name := styleDim.Render(fieldNames[i])
		mark := "  "
		if i == f.focus {
			name, mark = styleHeader.Render(fieldNames[i]), styleTree.Render("› ")
		}
		pad := strings.Repeat(" ", max(1, formLabel-lipgloss.Width(fieldNames[i])))
		lead := mark + name + pad
		switch i {
		case fieldTitle:
			t := f.title
			t.Width = w
			b.WriteString(lead + t.View() + "\n")
		case fieldDesc:
			d := f.desc
			d.SetWidth(w)
			indent := "\n" + strings.Repeat(" ", lipgloss.Width(lead))
			b.WriteString(lead + strings.ReplaceAll(d.View(), "\n", indent) + "\n")
		default:
			b.WriteString(lead + m.viewChosen(i, i == f.focus) + "\n")
		}
	}
	b.WriteString("\n")
	if f.picking {
		room := m.height - strings.Count(b.String(), "\n") - 6 // tabs, blank, heading, filter; status, footer
		b.WriteString(m.viewPick(max(1, room)))
	}
	return b.String()
}

func (m model) viewChosen(field int, focused bool) string {
	var out string
	switch {
	case field == fieldTeam && m.teamList == nil:
		out = m.spin.View() + styleDim.Render(" loading…")
	case field == fieldStatus && m.form.teamID != "" && len(m.states[m.form.teamID]) == 0:
		out = m.spin.View() + styleDim.Render(" loading…")
	default:
		id := m.chosen(field)
		for _, c := range m.choices(field) {
			if c.id == id {
				out = c.glyph + " " + shorten(c.label, max(10, m.width-formLabel-14))
			}
		}
		if out == "" {
			out = styleDim.Render("none")
		}
	}
	if focused {
		out += styleDim.Render("   ←→ change · enter list")
	}
	return out
}

func (m model) viewPick(room int) string {
	f := m.form
	cs := m.picked()
	var b strings.Builder
	b.WriteString(styleHeader.Render(" "+fieldNames[f.focus]) + "  " + f.pick.View() + "\n")
	start := 0
	if f.pickCursor >= room {
		start = f.pickCursor - room + 1
	}
	for i := start; i < len(cs) && i-start < room; i++ {
		line := "   " + cs[i].glyph + " " + shorten(cs[i].label, max(10, m.width-8))
		if i == f.pickCursor {
			line = highlight(" ›"+line[2:], max(20, m.width-2))
		}
		b.WriteString(line + "\n")
	}
	if len(cs) == 0 {
		b.WriteString(styleDim.Render("   Nothing matches.") + "\n")
	}
	return b.String()
}

func (m model) viewCreated() string {
	is := m.created
	w := max(20, m.width-2)
	var b strings.Builder
	b.WriteString(" " + styleOK.Render("✓") + " Created " + styleHeader.Render(is.Identifier) + "\n\n")
	b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(1).Bold(true).Render(is.Title) + "\n\n")
	b.WriteString(field("Status", colored(stateIcon(is.State), is.State.Color)+" "+is.State.Name))
	if is.Project != nil {
		b.WriteString(field("Project", is.Project.Name))
	}
	who := styleDim.Render("nobody")
	if is.Assignee != nil {
		who = is.Assignee.Name
		if is.Assignee.IsMe {
			who += styleDim.Render(" (you)")
		}
	}
	b.WriteString(field("Assignee", who))
	b.WriteString(field("Branch", issueTarget(*is).Branch))
	b.WriteString("\n " + styleDim.Render("enter to view it, s to start it now, c to add another.") + "\n")
	return b.String()
}

func (m model) createFooter() []hint {
	if m.screen == screenCreated {
		return []hint{{"enter view", "enter", hintGo}, {"s start", "s", hintAct}, {"c another", "c", hintAct}, {"o open in Linear", "o", hintView}, {"esc done", "esc", hintQuiet}}
	}
	f := m.form
	switch {
	case f.picking:
		return []hint{{"↑↓ choose", "", hintView}, {"enter pick", "enter", hintGo}, {"esc back", "esc", hintQuiet}}
	case f.focus == fieldTitle:
		return []hint{{"enter create", "enter", hintAct}, {"tab next", "tab", hintView}, {"esc cancel", "esc", hintQuiet}}
	default:
		return []hint{{"^s create", "ctrl+s", hintAct}, {"tab next", "tab", hintView}, {"esc cancel", "esc", hintQuiet}}
	}
}
