package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mkIssue(id, stateType, stateName string, pos float64, prio int) issue {
	is := issue{Identifier: id, Title: "title " + id, Priority: prio}
	is.State = workflowState{Name: stateName, Type: stateType, Position: pos}
	return is
}

func TestSortIssuesPutsWorkInFlightFirst(t *testing.T) {
	is := []issue{
		mkIssue("B1", "backlog", "Backlog", 0, 0),
		mkIssue("T1", "unstarted", "Todo", 1, 3),
		mkIssue("P1", "started", "In Progress", 2, 0),
		mkIssue("R1", "started", "In Review", 3, 0),
		mkIssue("T2", "unstarted", "Todo", 1, 1),
		mkIssue("X1", "triage", "Triage", 0, 0),
	}
	sortIssues(is)
	var got []string
	for _, i := range is {
		got = append(got, i.Identifier)
	}
	want := "R1 P1 T2 T1 X1 B1"
	if strings.Join(got, " ") != want {
		t.Fatalf("order %v, want %s", got, want)
	}
}

func TestIssueMatchesEveryWordAnywhere(t *testing.T) {
	is := mkIssue("ACT-12", "started", "In Progress", 0, 0)
	is.Title = "Briefing shows work that moved"
	is.Project = &projectRef{Name: "Daily brief"}
	for q, want := range map[string]bool{
		"": true, "act-12": true, "brief moved": true, "progress": true,
		"daily": true, "brief nope": false,
	} {
		if is.matches(q) != want {
			t.Errorf("matches(%q) = %v", q, !want)
		}
	}
}

// fakeLinear serves GraphQL; each call gets the next canned reply.
func fakeLinear(t *testing.T, replies ...func(w http.ResponseWriter, r *http.Request)) *[]string {
	t.Helper()
	var auths []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if n >= len(replies) {
			t.Errorf("unexpected extra request %d", n+1)
			w.WriteHeader(500)
			return
		}
		replies[n](w, r)
		n++
	}))
	old := graphqlURL
	graphqlURL = srv.URL
	t.Cleanup(func() { graphqlURL = old; srv.Close() })
	return &auths
}

func reply(status int, body any) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
}

func TestQueryRefreshesOnceOnAuthErrorThenRetries(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "stale", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "fresh", "refresh_token": "rt2", "expires_in": 3600}
	})
	auths := fakeLinear(t,
		reply(400, map[string]any{"errors": []any{map[string]any{"message": "auth", "extensions": map[string]any{"code": "AUTHENTICATION_ERROR"}}}}),
		reply(200, map[string]any{"data": map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{"nodes": []any{
			map[string]any{"identifier": "ACT-1", "title": "x", "state": map[string]any{"type": "started", "name": "In Progress"}},
		}}}}}),
	)
	c := &linearClient{cfg: config{ClientID: "cid"}}
	is, err := c.myIssues(context.Background())
	if err != nil || len(is) != 1 || is[0].Identifier != "ACT-1" {
		t.Fatalf("got %v, %v", is, err)
	}
	if (*auths)[0] != "Bearer stale" || (*auths)[1] != "Bearer fresh" {
		t.Errorf("auth headers %v", *auths)
	}
}

func TestQueryGivesUpAfterSecondAuthError(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "a", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "b", "refresh_token": "rt2", "expires_in": 3600}
	})
	fakeLinear(t, reply(401, map[string]any{}), reply(401, map[string]any{}))
	c := &linearClient{cfg: config{}}
	if _, err := c.myIssues(context.Background()); !errors.Is(err, errSignedOut) {
		t.Fatalf("got %v, want errSignedOut", err)
	}
}

func TestQuerySurfacesGraphQLErrors(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)})
	fakeLinear(t, reply(200, map[string]any{"errors": []any{map[string]any{"message": "Cannot query field"}}}))
	c := &linearClient{cfg: config{}}
	if _, err := c.projects(context.Background()); err == nil || !strings.Contains(err.Error(), "Cannot query field") {
		t.Fatalf("got %v", err)
	}
}

func TestStartIssueMovesToFirstStartedStateAndClaims(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)})
	var mutation map[string]any
	fakeLinear(t,
		reply(200, map[string]any{"data": map[string]any{"team": map[string]any{"states": map[string]any{"nodes": []any{
			map[string]any{"id": "review", "name": "In Review", "type": "started", "position": 3},
			map[string]any{"id": "progress", "name": "In Progress", "type": "started", "position": 2},
		}}}}}),
		reply(200, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "me"}}}),
		func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Variables map[string]any `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mutation = body.Variables
			reply(200, map[string]any{"data": map[string]any{"issueUpdate": map[string]any{"success": true}}})(w, r)
		},
	)
	is := mkIssue("ACT-9", "unstarted", "Todo", 1, 0)
	is.ID = "issue-9"
	if err := (&linearClient{}).startIssue(context.Background(), is); err != nil {
		t.Fatal(err)
	}
	input := mutation["input"].(map[string]any)
	if mutation["id"] != "issue-9" || input["stateId"] != "progress" || input["assigneeId"] != "me" {
		t.Fatalf("mutation variables %v", mutation)
	}
}

func TestStartIssueLeavesStartedAndOwnedIssuesAlone(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)})
	fakeLinear(t) // any request fails the test
	is := mkIssue("ACT-9", "started", "In Review", 3, 0)
	is.Assignee = &person{ID: "someone"}
	if err := (&linearClient{}).startIssue(context.Background(), is); err != nil {
		t.Fatal(err)
	}
}
