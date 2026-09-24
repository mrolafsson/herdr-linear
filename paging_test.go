package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// issuePage serves n issues starting at from, saying whether more follow.
func issuePage(from, n int, more bool) map[string]any {
	var nodes []any
	for i := from; i < from+n; i++ {
		nodes = append(nodes, map[string]any{
			"id": fmt.Sprint("i", i), "identifier": fmt.Sprint("ENG-", i), "title": "t",
			"state": map[string]any{"type": "unstarted", "name": "Todo"},
		})
	}
	return map[string]any{"data": map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{
		"nodes":    nodes,
		"pageInfo": map[string]any{"hasNextPage": more, "endCursor": fmt.Sprint("c", from+n)},
	}}}}
}

func TestListsFollowTheCursorToTheEnd(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)})
	var afters []any
	page := func(from int, more bool) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Variables map[string]any `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			afters = append(afters, body.Variables["after"])
			_ = json.NewEncoder(w).Encode(issuePage(from, 100, more))
		}
	}
	fakeLinear(t, page(0, true), page(100, true), page(200, false))
	is, err := (&linearClient{}).myIssues(context.Background())
	if err != nil || len(is) != 300 {
		t.Fatalf("%d issues, %v", len(is), err)
	}
	if afters[0] != nil || afters[1] != "c100" || afters[2] != "c200" {
		t.Fatalf("cursors sent: %v", afters)
	}
}

// Round 2: a server that says "more" but never moves must not page forever.
func TestPagingThatDoesntAdvanceStops(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)})
	stuck := func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{
			"nodes": []any{}, "pageInfo": map[string]any{"hasNextPage": true, "endCursor": "same"},
		}}}})
	}
	repeat := func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(issuePage(0, 5, true)) // endCursor "c5" every time
	}
	cursor := func(c string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) {
			page := issuePage(0, 1, true)
			page["data"].(map[string]any)["viewer"].(map[string]any)["assignedIssues"].(map[string]any)["pageInfo"] =
				map[string]any{"hasNextPage": true, "endCursor": c}
			_ = json.NewEncoder(w).Encode(page)
		}
	}
	for name, pages := range map[string][]func(http.ResponseWriter, *http.Request){
		"empty page":  {stuck},
		"same cursor": {repeat, repeat},
		// Round 2b: A → B → A never repeats the *last* cursor.
		"cursor cycle": {cursor("A"), cursor("B"), cursor("A")},
	} {
		t.Run(name, func(t *testing.T) {
			fakeLinear(t, pages...)
			done := make(chan error, 1)
			go func() { _, err := (&linearClient{}).myIssues(context.Background()); done <- err }()
			select {
			case err := <-done:
				if !errors.Is(err, errIncomplete) {
					t.Fatalf("want errIncomplete, got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("paged forever")
			}
		})
	}
}

func TestHugeListsStopVisiblyNotSilently(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)})
	var pages []func(http.ResponseWriter, *http.Request)
	for i := 0; i < maxItems/100; i++ {
		from := i * 100
		pages = append(pages, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(issuePage(from, 100, true))
		})
	}
	fakeLinear(t, pages...)
	is, err := (&linearClient{}).myIssues(context.Background())
	if !errors.Is(err, errTruncated) || len(is) != maxItems {
		t.Fatalf("%d issues, %v: want %d and errTruncated", len(is), err, maxItems)
	}

	// The picker shows what it has, and says it's not everything.
	m := newModel(context.Background(), config{}, "")
	m.width, m.height = 100, 20
	next, _ := m.Update(issuesMsg{issues: is, err: err})
	m = next.(model)
	if len(m.issues) != maxItems || m.mode != modeList || m.err == "" {
		t.Fatalf("issues %d mode %v err %q", len(m.issues), m.mode, m.err)
	}
}
