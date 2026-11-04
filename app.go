package main

// The Herdr app's Linear mod runs this binary as `herdr-linear app JSON`: one
// request in, one JSON answer out. Linear's side (lists, an issue, changes)
// runs on the machine you're signed in on; a worktree's (git) on the machine
// with the checkout, where no sign-in is needed. It's the popup's own code
// underneath. A request that can outlast the app's 30 seconds (a worktree's
// fetch) runs as a job in the background, and the mod asks after it.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// appVersion is this build's version, as herdr-plugin.toml has it (a test
// holds them together): the mod checks it's the one it pinned.
const appVersion = "0.14.2"

// appRequest is what the mod asks.
type appRequest struct {
	Op        string `json:"op"`
	Demo      bool   `json:"demo,omitempty"`
	Workspace string `json:"workspace,omitempty"` // its id; "" for the only (or first) one
	Tab       string `json:"tab,omitempty"`       // list: mine, recent, projects, project
	Project   string `json:"project,omitempty"`   // list project, project: its id
	Issue     string `json:"issue,omitempty"`     // detail, set-state, check-start, start: its id
	Key       string `json:"key,omitempty"`       // find: "ENG-142", or a number
	State     string `json:"state,omitempty"`     // set-state: the state's id
	Team      string `json:"team,omitempty"`      // states: the team's id
	// start: what the worktree was made for, to check it still is.
	Branch  string   `json:"branch,omitempty"`
	TeamKey string   `json:"teamKey,omitempty"`
	New     *appNew  `json:"new,omitempty"`    // create
	Target  *appTree `json:"target,omitempty"` // worktree
	Repo    string   `json:"repo,omitempty"`   // worktree: exact absolute base checkout; overrides config and dirs
	Dirs    []string `json:"dirs,omitempty"`   // worktree: where you are (the shown space's folder)
	Path    string   `json:"path,omitempty"`   // unworktree
	BG      bool     `json:"bg,omitempty"`
	Job     string   `json:"job,omitempty"`
}

// appNew is a new issue, as the mod's form has it.
type appNew struct {
	Team        string `json:"team"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Project     string `json:"project"`
	State       string `json:"state"`
	Priority    int    `json:"priority"`
	AssignToMe  bool   `json:"assignToMe"`
}

// appTree is what a worktree is for: an issue's branch, or a project's.
type appTree struct {
	Branch       string `json:"branch"`
	Label        string `json:"label"`
	TeamKey      string `json:"teamKey"`
	WorkspaceKey string `json:"workspaceKey"` // its URL key: which "repos" in config.json apply
	// For the prompt start sends: the issue's, or the project's.
	Identifier string `json:"identifier,omitempty"`
	Title      string `json:"title,omitempty"`
	URL        string `json:"url,omitempty"`
	Project    string `json:"project,omitempty"` // a project's name: its start prompt instead
}

// appIssue is an issue as the mod lists it.
type appIssue struct {
	ID         string        `json:"id"`
	Identifier string        `json:"identifier"`
	Title      string        `json:"title"`
	URL        string        `json:"url"`
	Branch     string        `json:"branch"`
	Priority   int           `json:"priority"`
	State      workflowState `json:"state"`
	TeamID     string        `json:"teamId"`
	TeamKey    string        `json:"teamKey"`
	Project    *projectRef   `json:"project,omitempty"`
	Assignee   string        `json:"assignee,omitempty"`
	Mine       bool          `json:"mine"`
	Group      string        `json:"group"` // the heading it's listed under
}

func issueOf(is issue) appIssue {
	out := appIssue{
		ID: is.ID, Identifier: is.Identifier, Title: clean(is.Title, false), URL: is.URL, Branch: issueTarget(is).Branch,
		Priority: is.Priority, State: is.State, TeamID: is.Team.ID, TeamKey: is.Team.Key, Project: is.Project, Group: is.State.Name,
	}
	out.State.Name = clean(out.State.Name, false)
	if is.Project != nil {
		p := *is.Project
		p.Name = clean(p.Name, false)
		out.Project = &p
	}
	if is.Assignee != nil {
		out.Assignee, out.Mine = clean(is.Assignee.Name, false), is.Assignee.IsMe
	}
	return out
}

// appProject is a project as the mod lists it.
type appProject struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	URL        string        `json:"url"`
	Color      string        `json:"color"`
	Progress   float64       `json:"progress"`
	TargetDate string        `json:"targetDate"`
	Status     projectStatus `json:"status"`
	Lead       string        `json:"lead,omitempty"`
	Teams      []string      `json:"teams"`
	Branch     string        `json:"branch"`
	TeamKey    string        `json:"teamKey"` // its one team, or ""
}

func projectOf(p project) appProject {
	t := projectTarget(p)
	out := appProject{ID: p.ID, Name: clean(p.Name, false), URL: p.URL, Color: p.Color, Progress: p.Progress, TargetDate: p.TargetDate,
		Status: p.Status, Teams: []string{}, Branch: t.Branch, TeamKey: t.TeamKey}
	if p.Lead != nil {
		out.Lead = clean(p.Lead.Name, false)
	}
	for _, tk := range p.Teams.Nodes {
		out.Teams = append(out.Teams, tk.Key)
	}
	return out
}

func runApp(ctx context.Context, cfg config, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("app needs one JSON request")
	}
	var req appRequest
	if err := json.Unmarshal([]byte(args[0]), &req); err != nil {
		return fmt.Errorf("app: %w", err)
	}
	var out any
	var err error
	if req.BG && req.Job != "" && jobID.MatchString(req.Job) {
		markJobRunning(req.Job) // this is the job: it has begun
	}
	switch {
	case req.BG && req.Job == "":
		out, err = startJob(req)
	case req.Op == "job":
		out, err = jobAnswer(req.Job)
	default:
		out, err = appAnswer(ctx, cfg, req)
	}
	if err != nil {
		out = appError(err)
	}
	if req.BG && req.Job != "" {
		return finishJob(req.Job, out)
	}
	return json.NewEncoder(stdout).Encode(out)
}

func appError(err error) map[string]any {
	e := map[string]any{"error": err.Error()}
	if errors.Is(err, errSignedOut) {
		e["signedOut"] = true
	}
	var nr *noRepoError
	if errors.As(err, &nr) {
		e["noRepo"] = true
	}
	return e
}

// appClient is the workspace a request names, signed in to.
func appClient(ctx context.Context, cfg config, req appRequest) (source, *workspace, *appDemo, error) {
	if req.Demo {
		d := loadAppDemo()
		return d.src, &workspace{ID: "w-halcyon", Name: "Halcyon", URLKey: "halcyon"}, d, nil
	}
	ix, err := loadWorkspaces(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(ix.Workspaces) == 0 {
		return nil, nil, nil, errSignedOut
	}
	w := &ix.Workspaces[0]
	if req.Workspace != "" {
		if w = ix.byID(req.Workspace); w == nil {
			return nil, nil, nil, fmt.Errorf("not signed in to that workspace any more: %w", errSignedOut)
		}
	}
	return &linearClient{cfg: cfg.forWorkspace(w.URLKey), ws: *w}, w, nil, nil
}

func appAnswer(ctx context.Context, cfg config, req appRequest) (any, error) {
	switch req.Op {
	case "status":
		return appStatus(ctx, cfg, req.Demo)
	case "worktree":
		if req.Target == nil {
			return nil, errors.New("worktree needs a target")
		}
		if req.Demo {
			return demoWorktree(*req.Target), nil
		}
		return appWorktree(ctx, cfg, *req.Target, req.Dirs, req.Repo)
	case "unworktree":
		if req.Demo {
			return map[string]any{"ok": true}, nil
		}
		return map[string]any{"ok": true}, removeAppWorktree(ctx, req.Path)
	}

	src, w, demo, err := appClient(ctx, cfg, req)
	if err != nil {
		return nil, err
	}
	saved := func(v any) (any, error) {
		if demo != nil {
			if err := demo.save(); err != nil {
				return nil, err
			}
		}
		return v, nil
	}
	switch req.Op {
	case "list":
		var is []issue
		switch req.Tab {
		case "mine":
			is, err = src.myIssues(ctx)
		case "recent":
			is, err = src.createdIssues(ctx)
		case "project":
			is, err = src.projectIssues(ctx, req.Project)
		case "projects":
			ps, err := src.projects(ctx)
			if err != nil && len(ps) == 0 {
				return nil, err
			}
			out := []appProject{}
			for _, p := range ps {
				out = append(out, projectOf(p))
			}
			return listAnswer("projects", out, err), nil
		default:
			return nil, fmt.Errorf("no tab %q", req.Tab)
		}
		if err != nil && len(is) == 0 {
			return nil, err
		}
		out := []appIssue{}
		for _, x := range is {
			out = append(out, issueOf(x))
		}
		if req.Tab == "recent" {
			for i := range out {
				out[i].Group = "" // newest first, as filed: no status groups
			}
		}
		return listAnswer("issues", out, err), nil
	case "find":
		teamKey, n := splitIssueNumber(strings.ToUpper(strings.TrimSpace(req.Key)))
		if n == 0 {
			return nil, fmt.Errorf("%q isn't an issue's key", req.Key)
		}
		is, err := src.issuesByNumber(ctx, n, teamKey)
		if err != nil {
			return nil, err
		}
		out := []appIssue{}
		for _, x := range is {
			out = append(out, issueOf(x))
		}
		return map[string]any{"issues": out}, nil
	case "detail":
		is, err := src.freshIssue(ctx, req.Issue)
		if err != nil {
			return nil, err
		}
		d, err := src.issueDetail(ctx, req.Issue)
		if err != nil {
			return nil, err
		}
		states, err := src.teamStates(ctx, is.Team.ID)
		if err != nil {
			return nil, err
		}
		labels := []label{}
		for _, l := range d.Labels.Nodes {
			labels = append(labels, label{Name: clean(l.Name, false), Color: l.Color})
		}
		out := map[string]any{
			"issue": issueOf(is), "description": clean(d.Description, true), "priorityLabel": d.PriorityLabel,
			"dueDate": d.DueDate, "labels": labels, "states": states, "workspaceKey": w.URLKey,
		}
		if d.Cycle != nil {
			name := d.Cycle.Name
			if name == "" {
				name = fmt.Sprintf("Cycle %d", d.Cycle.Number)
			}
			out["cycle"] = clean(name, false)
		}
		if err := startConflict(is); err != nil {
			out["startWhy"] = err.Error()
		}
		out["prs"] = pullRequestsOf(d.Attachments.Nodes)
		return out, nil
	case "project":
		d, err := src.projectDetail(ctx, req.Project)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"description": clean(d.Description, false), "content": clean(d.Content, true), "startDate": d.StartDate}
		if d.Lead != nil {
			out["lead"] = clean(d.Lead.Name, false)
		}
		return out, nil
	case "set-state":
		if err := src.setState(ctx, req.Issue, req.State); err != nil {
			return nil, err
		}
		is, err := src.freshIssue(ctx, req.Issue)
		if err != nil {
			return nil, err
		}
		return saved(map[string]any{"issue": issueOf(is), "note": is.Identifier + " is " + is.State.Name})
	case "check-start":
		// Start's first step: Linear's copy now, and whether it may be started.
		is, err := src.freshIssue(ctx, req.Issue)
		if err != nil {
			return nil, fmt.Errorf("couldn't check it before starting it: %w", err)
		}
		if err := startConflict(is); err != nil {
			return nil, err
		}
		return map[string]any{"issue": issueOf(is)}, nil
	case "start":
		// After the worktree: read again, and only then move it along.
		is, err := src.freshIssue(ctx, req.Issue)
		if err != nil {
			return nil, err
		}
		if err := startConflict(is); err != nil {
			return nil, fmt.Errorf("it changed meanwhile: %w", err)
		}
		if t := issueTarget(is); (req.Branch != "" && t.Branch != req.Branch) || (req.TeamKey != "" && t.TeamKey != req.TeamKey) {
			return nil, errors.New(is.Identifier + "'s branch or team changed meanwhile, so it wasn't started: open it again for the right worktree")
		}
		if err := src.startIssue(ctx, is); err != nil {
			return nil, err
		}
		is, _ = src.freshIssue(ctx, req.Issue)
		return saved(map[string]any{"issue": issueOf(is), "note": is.Identifier + " is " + is.State.Name})
	case "teams":
		ts, err := src.teams(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"teams": ts, "priorities": priorityNames}, nil
	case "states":
		states, err := src.teamStates(ctx, req.Team)
		if err != nil {
			return nil, err
		}
		return map[string]any{"states": states}, nil
	case "create":
		n := req.New
		if n == nil || strings.TrimSpace(n.Title) == "" {
			return nil, errors.New("give it a title first")
		}
		if n.Team == "" {
			return nil, errors.New("choose a team first")
		}
		is, err := src.createIssue(ctx, newIssue{TeamID: n.Team, Title: strings.TrimSpace(n.Title), Description: strings.TrimSpace(n.Description),
			ProjectID: n.Project, StateID: n.State, Priority: n.Priority, AssignToMe: n.AssignToMe})
		if err != nil {
			return nil, err
		}
		return saved(map[string]any{"issue": issueOf(is), "note": "Created " + is.Identifier})
	}
	return nil, fmt.Errorf("no op %q", req.Op)
}

// appPR is a pull request an issue is linked to.
type appPR struct {
	Host   string `json:"host"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

var pullURL = regexp.MustCompile(`^https://([^/]+)/([^/]+/[^/]+)/pull/(\d+)`)

// pullRequestsOf picks the pull requests out of an issue's links: GitHub's
// (github.com or an Enterprise host), as Linear's integration attaches them.
func pullRequestsOf(as []attachment) []appPR {
	out := []appPR{}
	seen := map[string]bool{}
	for _, a := range as {
		m := pullURL.FindStringSubmatch(a.URL)
		if m == nil || seen[m[0]] {
			continue
		}
		seen[m[0]] = true
		n := 0
		fmt.Sscan(m[3], &n)
		out = append(out, appPR{Host: m[1], Repo: m[2], Number: n, Title: clean(a.Title, false), URL: a.URL})
	}
	return out
}

func listAnswer(key string, list any, err error) map[string]any {
	out := map[string]any{key: list}
	if err != nil {
		out["warning"] = err.Error() // what came back shows, with what's missing
	}
	return out
}

// appStatus is the workspaces you're signed in to here.
func appStatus(ctx context.Context, cfg config, demo bool) (any, error) {
	if demo {
		return map[string]any{"workspaces": []workspace{{ID: "w-halcyon", Name: "Halcyon", URLKey: "halcyon"}}, "version": appVersion}, nil
	}
	ix, err := loadWorkspaces(ctx, cfg)
	if err != nil {
		return nil, err
	}
	ws := []workspace{}
	for _, w := range ix.Workspaces {
		if _, err := readStore(account(w.ID)); err == nil {
			ws = append(ws, workspace{ID: w.ID, Name: clean(w.Name, false), URLKey: w.URLKey})
		}
	}
	return map[string]any{"workspaces": ws, "version": appVersion}, nil
}

// ── worktrees ─────────────────────────────────────────────────────────────────

// noRepoError: there's no repo to make the worktree in.
type noRepoError struct{ msg string }

func (e *noRepoError) Error() string { return e.msg }

// worktreeRoot is where the app keeps worktrees, as its own worktree mod
// does: <root>/<repo>/<branch, slashes as dashes>.
func worktreeRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".herdr", "worktrees")
}

var slugJunk = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func slug(branch string) string { return strings.Trim(slugJunk.ReplaceAllString(branch, "-"), "-") }

// appWorktree opens t's worktree, making it from the base branch if there's
// none: in an explicit repo, else the team's configured repo, else dirs.
// An invalid explicit repo fails closed; it never falls back to another checkout.
func appWorktree(ctx context.Context, cfg config, t appTree, dirs []string, explicitRepo string) (any, error) {
	cfg = cfg.forWorkspace(t.WorkspaceKey)
	repo := explicitRepo
	if repo != "" {
		if !filepath.IsAbs(repo) || strings.ContainsRune(repo, 0) {
			return nil, errors.New("repo must be an absolute Git checkout path without NUL bytes")
		}
		real, err := filepath.EvalSymlinks(repo)
		if err != nil {
			return nil, fmt.Errorf("invalid repo %q: %w", repo, err)
		}
		top, err := gitOutput(ctx, repo, "rev-parse", "--show-toplevel")
		if err != nil || strings.TrimSuffix(top, "\n") != real {
			return nil, fmt.Errorf("repo %q must be the top level of a Git checkout", repo)
		}
	}
	if dir, ok := cfg.Repos[t.TeamKey]; repo == "" && ok && dir != "" {
		repo = expandHome(dir)
	}
	for _, d := range dirs {
		if repo != "" {
			break
		}
		if top, err := gitOutput(ctx, expandHome(d), "rev-parse", "--show-toplevel"); err == nil {
			repo = strings.TrimSuffix(top, "\n")
		}
	}
	if repo == "" {
		where := "map team " + t.TeamKey + " to its repo under \"repos\" in " + filepath.Join(configDir(), "config.json")
		if t.TeamKey == "" {
			where = "this project spans several teams, so it has no one repo"
		}
		return nil, &noRepoError{"Open it from a space in the repo it belongs to, or " + where + "."}
	}
	prompt := appPrompt(cfg, t, repo)
	if path := gitWorktreeOn(ctx, repo, t.Branch); path != "" {
		return map[string]any{"path": path, "created": false, "prompt": prompt}, nil
	}
	main := repo
	if common, err := gitOutput(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		main = filepath.Dir(strings.TrimSuffix(common, "\n"))
	}
	path := filepath.Join(worktreeRoot(), filepath.Base(main), slug(t.Branch))
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s is already there, and isn't %s's worktree", path, t.Branch)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// A branch that already exists (its worktree was removed) is checked out
	// as it is; only a new branch needs a base.
	if _, err := git(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+t.Branch); err == nil {
		if _, err := git(ctx, repo, "worktree", "add", path, t.Branch); err != nil {
			return nil, err
		}
	} else if _, err := git(ctx, repo, "worktree", "add", "--no-track", "-b", t.Branch, path, baseRef(ctx, cfg, repo)); err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return map[string]any{"path": path, "created": true, "prompt": appPrompt(cfg, t, path, repo)}, nil
}

// appPrompt is start's first message to the worktree's agent, as the
// popup's: the project's, or /ticket where Claude Code has one.
func appPrompt(cfg config, t appTree, dirs ...string) string {
	if t.Project != "" {
		return expandProjectPrompt(cfg.ProjectStartPrompt, project{Name: t.Project, URL: t.URL})
	}
	return startPrompt(cfg, issue{Identifier: t.Identifier, Title: t.Title, URL: t.URL}, dirs...)
}

// gitWorktreeOn is the linked worktree of repo on branch, or "".
func gitWorktreeOn(ctx context.Context, repo, branch string) string {
	out, err := gitOutput(ctx, repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return ""
	}
	for i, block := range strings.Split(out, "\x00\x00") {
		if i == 0 {
			continue // the main checkout is the repo itself, whatever it's on
		}
		var path, b string
		for _, l := range strings.Split(block, "\x00") {
			if p, ok := strings.CutPrefix(l, "worktree "); ok {
				path = p
			}
			if r, ok := strings.CutPrefix(l, "branch "); ok {
				b = strings.TrimPrefix(r, "refs/heads/")
			}
		}
		if path != "" && b == branch {
			return path
		}
	}
	return ""
}

// removeAppWorktree removes a worktree once its issue is done, never forcing:
// uncommitted changes keep it where it is.
func removeAppWorktree(ctx context.Context, path string) error {
	if path == "" {
		return errors.New("which worktree?")
	}
	common, err := gitOutput(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	main := filepath.Dir(strings.TrimSuffix(common, "\n"))
	if filepath.Clean(main) == filepath.Clean(path) {
		return errors.New(path + " is the repo's own checkout, not a worktree")
	}
	_, err = git(ctx, main, "worktree", "remove", path)
	return err
}

func demoWorktree(t appTree) map[string]any {
	return map[string]any{
		"path": "~/.herdr/worktrees/notes-app/" + slug(t.Branch), "created": true, "demo": true,
		"note":   "Demo: would create a worktree on " + t.Branch,
		"prompt": appPrompt(config{}, t),
	}
}

// ── the demo ──────────────────────────────────────────────────────────────────

// appDemo is the demo with what was done to it kept between requests, each
// of which is its own run: an issue moved to In Review stays there.
type appDemo struct{ src *demoSource }

type demoSaved struct {
	Issues  []issue                 `json:"issues"`
	Created []string                `json:"created"`
	Details map[string]*issueDetail `json:"details"`
}

func appDemoPath() string { return filepath.Join(stateDir(), "app-demo.json") }

func loadAppDemo() *appDemo {
	d := &appDemo{src: newDemoSource()}
	var s demoSaved
	if data, err := os.ReadFile(appDemoPath()); err == nil && json.Unmarshal(data, &s) == nil && len(s.Issues) > 0 {
		d.src.issues, d.src.created = s.Issues, s.Created
		for id, det := range s.Details {
			d.src.details[id] = det
		}
	}
	return d
}

func (d *appDemo) save() error {
	d.src.mu.Lock()
	s := demoSaved{Issues: d.src.issues, Created: d.src.created, Details: map[string]*issueDetail{}}
	for _, id := range d.src.created {
		if det := d.src.details[id]; det != nil {
			s.Details[id] = det
		}
	}
	data, err := json.Marshal(s)
	d.src.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(appDemoPath(), data, 0o600)
}

// ── jobs ──────────────────────────────────────────────────────────────────────

func jobPath(id string) string { return filepath.Join(stateDir(), "app-jobs", id+".json") }

// jobLogPath is where a job's own output goes: why it stopped, if it did.
func jobLogPath(id string) string { return filepath.Join(stateDir(), "app-jobs", id+".log") }

// jobState is a job's file while it runs: the process doing it, once it's
// begun, so one that stopped without an answer is told from one at work.
type jobState struct {
	Done    bool  `json:"done"`
	PID     int   `json:"pid,omitempty"`
	Started int64 `json:"started,omitempty"` // unix seconds
	Asked   int64 `json:"asked,omitempty"`
}

// jobStartWait is how long a job may take to begin before it's given up on.
const jobStartWait = 15 * time.Second

// markJobRunning is the job saying it has begun, and as which process.
func markJobRunning(id string) {
	data, _ := json.Marshal(jobState{PID: os.Getpid(), Started: time.Now().Unix()})
	_ = os.WriteFile(jobPath(id)+".tmp", data, 0o600)
	_ = os.Rename(jobPath(id)+".tmp", jobPath(id))
}

// logTail is the last line or two of a job's log, for why it stopped.
func logTail(id string) string {
	data, err := os.ReadFile(jobLogPath(id))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(clean(string(data), true)), "\n")
	if len(lines) > 2 {
		lines = lines[len(lines)-2:]
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

var jobID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// startJob runs the request again in the background, as a job of its own,
// and says which: the mod asks after it with {"op": "job"}.
func startJob(req appRequest) (any, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	req.Job = hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(jobPath(req.Job)), 0o700); err != nil {
		return nil, err
	}
	asked, _ := json.Marshal(jobState{Asked: time.Now().Unix()})
	if err := os.WriteFile(jobPath(req.Job), asked, 0o600); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(req)
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{"app", string(data)}
	logPath := jobLogPath(req.Job)
	if cmd := jobCmd(self, args, logPath); cmd != nil {
		// systemd-run: it returns once the job's unit is up.
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("couldn't start the job: %v %s", err, strings.TrimSpace(string(out)))
		}
	} else {
		cmd := exec.Command(self, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			cmd.Stdout, cmd.Stderr = logf, logf
			defer logf.Close()
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		_ = cmd.Process.Release()
	}
	return map[string]any{"job": req.Job}, nil
}

func finishJob(id string, out any) error {
	if !jobID.MatchString(id) {
		return errors.New("not a job")
	}
	data, err := json.Marshal(map[string]any{"done": true, "result": out})
	if err != nil {
		return err
	}
	tmp := jobPath(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, jobPath(id))
}

// jobAnswer is how a job is getting on; once it's done, its answer, which
// is then forgotten.
func jobAnswer(id string) (any, error) {
	if !jobID.MatchString(id) {
		return nil, errors.New("not a job")
	}
	data, err := os.ReadFile(jobPath(id))
	if err != nil {
		return nil, errors.New("that job is gone")
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out["done"] == true {
		_ = os.Remove(jobPath(id))
		_ = os.Remove(jobLogPath(id))
		return out, nil
	}
	// Not done: at work, or stopped without an answer, or never begun.
	var st jobState
	_ = json.Unmarshal(data, &st)
	gone := func(why string) (any, error) {
		_ = os.Remove(jobPath(id))
		if tail := logTail(id); tail != "" {
			why += ": " + tail
		}
		_ = os.Remove(jobLogPath(id))
		return map[string]any{"done": true, "result": map[string]any{"error": why}}, nil
	}
	switch {
	case st.PID > 0 && !processAlive(st.PID):
		// The job may have written its answer just now: look once more.
		if again, err := os.ReadFile(jobPath(id)); err == nil && strings.Contains(string(again), `"done":true`) {
			return jobAnswer(id)
		}
		return gone("it stopped without finishing")
	case st.PID == 0 && st.Asked > 0 && time.Since(time.Unix(st.Asked, 0)) > jobStartWait:
		return gone("it didn't start")
	}
	return out, nil
}

// processAlive says whether a process is still running.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
