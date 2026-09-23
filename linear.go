package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

var graphqlURL = "https://api.linear.app/graphql" // a var so tests can point it elsewhere

// source is everything the picker reads from and writes to Linear. The real
// one is linearClient; demoSource (demo.go) is a fictional workspace for
// screenshots and trying the plugin without an account.
type source interface {
	myIssues(ctx context.Context) ([]issue, error)
	projects(ctx context.Context) ([]project, error)
	projectIssues(ctx context.Context, projectID string) ([]issue, error)
	issueDetail(ctx context.Context, id string) (*issueDetail, error)
	projectDetail(ctx context.Context, id string) (*projectDetail, error)
	teamStates(ctx context.Context, teamID string) ([]workflowState, error)
	setState(ctx context.Context, issueID, stateID string) error
	startIssue(ctx context.Context, is issue) error
}

type linearClient struct {
	cfg config
}

type gqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

func isAuthError(status int, errs []gqlError) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	for _, e := range errs {
		if e.Extensions.Code == "AUTHENTICATION_ERROR" {
			return true
		}
	}
	return false
}

// query runs one GraphQL operation. A rejected token is refreshed once and the
// request retried; a second rejection means the grant is gone.
func (c *linearClient) query(ctx context.Context, q string, vars map[string]any, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := accessToken(ctx, c.cfg, attempt > 0)
		if err != nil {
			return err
		}
		status, errs, data, err := c.post(ctx, token, q, vars)
		if err != nil {
			return err
		}
		if isAuthError(status, errs) {
			if attempt == 0 {
				continue
			}
			return errSignedOut
		}
		if len(errs) > 0 {
			return fmt.Errorf("Linear: %s", errs[0].Message)
		}
		if status != http.StatusOK {
			return fmt.Errorf("Linear: HTTP %d", status)
		}
		return json.Unmarshal(data, out)
	}
	return errSignedOut
}

func (c *linearClient) post(ctx context.Context, token, q string, vars map[string]any) (int, []gqlError, json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return 0, nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	var reply struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil && res.StatusCode == http.StatusOK {
		return 0, nil, nil, fmt.Errorf("Linear: bad response: %w", err)
	}
	return res.StatusCode, reply.Errors, reply.Data, nil
}

// ── model ─────────────────────────────────────────────────────────────────────

type workflowState struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"` // triage | backlog | unstarted | started | completed | canceled
	Color    string  `json:"color"`
	Position float64 `json:"position"`
}

type team struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

type projectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type person struct {
	ID   string `json:"id"`
	Name string `json:"displayName"`
	IsMe bool   `json:"isMe"`
}

type issue struct {
	ID         string        `json:"id"`
	Identifier string        `json:"identifier"`
	Title      string        `json:"title"`
	BranchName string        `json:"branchName"`
	URL        string        `json:"url"`
	Priority   int           `json:"priority"`
	State      workflowState `json:"state"`
	Team       team          `json:"team"`
	Project    *projectRef   `json:"project"`
	Assignee   *person       `json:"assignee"`
}

type projectStatus struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // backlog | planned | started | paused | completed | canceled
	Color string `json:"color"`
}

type teamKey struct {
	Key string `json:"key"`
}

type project struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	SlugID     string        `json:"slugId"`
	URL        string        `json:"url"`
	Color      string        `json:"color"`
	Progress   float64       `json:"progress"`
	TargetDate string        `json:"targetDate"`
	Status     projectStatus `json:"status"`
	Lead       *person       `json:"lead"`
	Teams      struct {
		Nodes []teamKey `json:"nodes"`
	} `json:"teams"`
}

const issueFields = `
fragment IssueFields on Issue {
  id identifier title branchName url priority
  state { id name type color position }
  team { id key }
  project { id name }
  assignee { id displayName isMe }
}`

const openStates = `{ state: { type: { nin: ["completed", "canceled"] } } }`

func (c *linearClient) myIssues(ctx context.Context) ([]issue, error) {
	var res struct {
		Viewer struct {
			AssignedIssues struct {
				Nodes []issue `json:"nodes"`
			} `json:"assignedIssues"`
		} `json:"viewer"`
	}
	q := `query { viewer { assignedIssues(first: 150, orderBy: updatedAt, filter: ` + openStates + `) { nodes { ...IssueFields } } } }` + issueFields
	if err := c.query(ctx, q, nil, &res); err != nil {
		return nil, err
	}
	issues := res.Viewer.AssignedIssues.Nodes
	sortIssues(issues)
	return issues, nil
}

func (c *linearClient) projectIssues(ctx context.Context, projectID string) ([]issue, error) {
	var res struct {
		Project struct {
			Issues struct {
				Nodes []issue `json:"nodes"`
			} `json:"issues"`
		} `json:"project"`
	}
	q := `query($id: String!) { project(id: $id) { issues(first: 200, orderBy: updatedAt, filter: ` + openStates + `) { nodes { ...IssueFields } } } }` + issueFields
	if err := c.query(ctx, q, map[string]any{"id": projectID}, &res); err != nil {
		return nil, err
	}
	issues := res.Project.Issues.Nodes
	sortIssues(issues)
	return issues, nil
}

func (c *linearClient) projects(ctx context.Context) ([]project, error) {
	var res struct {
		Projects struct {
			Nodes []project `json:"nodes"`
		} `json:"projects"`
	}
	q := `query { projects(first: 100, orderBy: updatedAt, filter: { status: { type: { nin: ["completed", "canceled"] } } }) {
  nodes { id name slugId url color progress targetDate status { name type color } lead { isMe } teams(first: 5) { nodes { key } } }
} }`
	if err := c.query(ctx, q, nil, &res); err != nil {
		return nil, err
	}
	ps := res.Projects.Nodes
	sortProjects(ps)
	return ps, nil
}

// sortProjects puts projects you lead first, then orders by how far along
// they are in the lifecycle.
func sortProjects(ps []project) {
	sort.SliceStable(ps, func(i, j int) bool {
		li, lj := ps[i].Lead != nil && ps[i].Lead.IsMe, ps[j].Lead != nil && ps[j].Lead.IsMe
		if li != lj {
			return li
		}
		return projectRank(ps[i].Status.Type) < projectRank(ps[j].Status.Type)
	})
}

// startIssue moves the issue to its team's first "started" state (In Progress)
// and assigns it to you when nobody owns it. Already-started issues keep their
// state: moving In Review back to In Progress would lose information.
func (c *linearClient) startIssue(ctx context.Context, is issue) error {
	input := map[string]any{}
	if is.State.Type != "started" {
		states, err := c.teamStates(ctx, is.Team.ID)
		if err != nil {
			return err
		}
		// teamStates is in lifecycle order, so the first started one is In Progress.
		for _, s := range states {
			if s.Type == "started" {
				input["stateId"] = s.ID
				break
			}
		}
		if input["stateId"] == nil {
			return errors.New("team " + is.Team.Key + " has no started state")
		}
	}
	if is.Assignee == nil {
		var me struct {
			Viewer struct {
				ID string `json:"id"`
			} `json:"viewer"`
		}
		if err := c.query(ctx, `query { viewer { id } }`, nil, &me); err != nil {
			return err
		}
		input["assigneeId"] = me.Viewer.ID
	}
	if len(input) == 0 {
		return nil
	}
	return c.updateIssue(ctx, is.ID, input)
}

func (c *linearClient) setState(ctx context.Context, issueID, stateID string) error {
	return c.updateIssue(ctx, issueID, map[string]any{"stateId": stateID})
}

func (c *linearClient) updateIssue(ctx context.Context, issueID string, input map[string]any) error {
	var res struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	q := `mutation($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success } }`
	if err := c.query(ctx, q, map[string]any{"id": issueID, "input": input}, &res); err != nil {
		return err
	}
	if !res.IssueUpdate.Success {
		return errors.New("Linear refused the update")
	}
	return nil
}

// teamStates lists a team's workflow states in lifecycle order: triage,
// backlog, todo, in progress, done, canceled; Linear's position within each.
func (c *linearClient) teamStates(ctx context.Context, teamID string) ([]workflowState, error) {
	var res struct {
		Team struct {
			States struct {
				Nodes []workflowState `json:"nodes"`
			} `json:"states"`
		} `json:"team"`
	}
	q := `query($id: String!) { team(id: $id) { states(first: 50) { nodes { id name type color position } } } }`
	if err := c.query(ctx, q, map[string]any{"id": teamID}, &res); err != nil {
		return nil, err
	}
	states := res.Team.States.Nodes
	sort.SliceStable(states, func(i, j int) bool {
		if a, b := lifecycleRank(states[i].Type), lifecycleRank(states[j].Type); a != b {
			return a < b
		}
		return states[i].Position < states[j].Position
	})
	return states, nil
}

type label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type cycle struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
}

type issueDetail struct {
	Description   string `json:"description"`
	PriorityLabel string `json:"priorityLabel"`
	DueDate       string `json:"dueDate"`
	Labels        struct {
		Nodes []label `json:"nodes"`
	} `json:"labels"`
	Cycle *cycle `json:"cycle"`
}

func (c *linearClient) issueDetail(ctx context.Context, id string) (*issueDetail, error) {
	var res struct {
		Issue issueDetail `json:"issue"`
	}
	q := `query($id: String!) { issue(id: $id) { description priorityLabel dueDate labels(first: 10) { nodes { name color } } cycle { number name } } }`
	if err := c.query(ctx, q, map[string]any{"id": id}, &res); err != nil {
		return nil, err
	}
	return &res.Issue, nil
}

type projectDetail struct {
	Description string  `json:"description"`
	Content     string  `json:"content"`
	StartDate   string  `json:"startDate"`
	Lead        *person `json:"lead"`
}

func (c *linearClient) projectDetail(ctx context.Context, id string) (*projectDetail, error) {
	var res struct {
		Project projectDetail `json:"project"`
	}
	q := `query($id: String!) { project(id: $id) { description content startDate lead { displayName } } }`
	if err := c.query(ctx, q, map[string]any{"id": id}, &res); err != nil {
		return nil, err
	}
	return &res.Project, nil
}

// ── ordering ──────────────────────────────────────────────────────────────────

// Work in flight first, then what's next, then the pile.
func stateRank(t string) int {
	switch t {
	case "started":
		return 0
	case "unstarted":
		return 1
	case "triage":
		return 2
	case "backlog":
		return 3
	}
	return 4
}

// The order a status picker lists states in: the way work flows.
func lifecycleRank(t string) int {
	switch t {
	case "triage":
		return 0
	case "backlog":
		return 1
	case "unstarted":
		return 2
	case "started":
		return 3
	case "completed":
		return 4
	case "canceled":
		return 5
	}
	return 6
}

func projectRank(t string) int {
	switch t {
	case "started":
		return 0
	case "planned":
		return 1
	case "paused":
		return 2
	case "backlog":
		return 3
	}
	return 4
}

// Linear's priority: 1 urgent … 4 low, 0 none (sorts last).
func priorityRank(p int) int {
	if p == 0 {
		return 5
	}
	return p
}

func sortIssues(is []issue) {
	sort.SliceStable(is, func(i, j int) bool {
		a, b := is[i], is[j]
		if ra, rb := stateRank(a.State.Type), stateRank(b.State.Type); ra != rb {
			return ra < rb
		}
		// Within "started", later positions are further along (In Review after
		// In Progress): show those first, they're closest to done.
		if a.State.Position != b.State.Position {
			if a.State.Type == "started" {
				return a.State.Position > b.State.Position
			}
			return a.State.Position < b.State.Position
		}
		return priorityRank(a.Priority) < priorityRank(b.Priority)
	})
}

func (is issue) matches(filter string) bool {
	if filter == "" {
		return true
	}
	hay := strings.ToLower(is.Identifier + " " + is.Title + " " + is.State.Name)
	if is.Project != nil {
		hay += " " + strings.ToLower(is.Project.Name)
	}
	for _, word := range strings.Fields(strings.ToLower(filter)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}

func (p project) matches(filter string) bool {
	hay := strings.ToLower(p.Name + " " + p.Status.Name)
	for _, word := range strings.Fields(strings.ToLower(filter)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}
