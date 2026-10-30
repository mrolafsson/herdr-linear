package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIssuesArgs(t *testing.T) {
	for _, c := range []struct {
		args  []string
		keys  string
		force bool
		err   string
	}{
		{[]string{`["HAL-1","ops-2"]`}, "HAL-1 ops-2", false, ""},
		{[]string{"--force", `["HAL-1"]`}, "HAL-1", true, ""},
		{[]string{`["HAL-1"]`, "--force"}, "HAL-1", true, ""},
		{[]string{`[]`}, "", false, ""},
		{nil, "", false, "needs a JSON array"},
		{[]string{"HAL-1"}, "", false, "not a JSON array"},
		{[]string{`["HAL-1"]`, `["HAL-2"]`}, "", false, "one JSON array"},
		{[]string{"-f", `["HAL-1"]`}, "", false, "unknown option"},
	} {
		keys, force, err := issuesArgs(c.args)
		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.args, err, c.err)
			}
		case err != nil:
			t.Errorf("%q: %v", c.args, err)
		case strings.Join(keys, " ") != c.keys || force != c.force:
			t.Errorf("%q: %v %v, want %q %v", c.args, keys, force, c.keys, c.force)
		}
	}
}

func TestParseIssueKey(t *testing.T) {
	for key, want := range map[string]string{
		"HAL-2066":          "HAL-2066",
		"ops-170":           "OPS-170",
		" ENG-7 ":           "ENG-7",
		"me/eng-142-fix":    "",
		"ENG-142 and OPS-1": "",
		"ENG":               "",
		"ENG-0":             "",
		"ENG-12345678":      "",
		"":                  "",
	} {
		ref, ok := parseIssueKey(key)
		got := ""
		if ok {
			got = ref.String()
		}
		if got != want {
			t.Errorf("%q: %q, want %q", key, got, want)
		}
	}
}

// lookupWorld is labelWorld with two signed-in workspaces, Halcyon (HAL) and
// Halcyon Ops (OPS).
func lookupWorld(t *testing.T) *labelWorld {
	w := newLabelWorld(t)
	w.index = workspaceIndex{Workspaces: []workspace{
		{ID: "a", Name: "Halcyon", URLKey: "halcyon"},
		{ID: "v", Name: "Halcyon Ops", URLKey: "halcyon-ops"},
	}}
	w.teams = map[string][]string{"a": {"HAL"}, "v": {"OPS"}}
	w.issues = map[string]map[issueRef]taggedIssue{
		"a": {{"HAL", 2066}: {Identifier: "HAL-2066", Title: "Cold open", State: "In Progress", StateType: "started", URL: "https://linear.app/halcyon/issue/HAL-2066"}},
		"v": {{"OPS", 170}: {Identifier: "OPS-170", Title: "Clock in", State: "Done", StateType: "completed", URL: "https://linear.app/halcyon-ops/issue/OPS-170"}},
	}
	return w
}

// summary is one line per result: key, then the identifier, state type and
// workspace, or "-" for none, then the error if any.
func summary(rs []lookupResult) string {
	var lines []string
	for _, r := range rs {
		s := r.Key + " -"
		if r.Issue != nil {
			s = r.Key + " " + r.Issue.Identifier + "/" + r.Issue.StateType + "@" + r.Issue.Workspace
		}
		if r.Error != "" {
			s += " ! " + r.Error
		}
		lines = append(lines, s)
	}
	return strings.Join(lines, "; ")
}

func TestLookupIssues(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(w *labelWorld)
		keys  []string
		want  string
		asked string
	}{
		{
			name:  "each in its own workspace, in the order given",
			keys:  []string{"ops-170", "HAL-2066"},
			want:  "ops-170 OPS-170/completed@halcyon-ops; HAL-2066 HAL-2066/started@halcyon",
			asked: "teams a, teams v, issues a HAL-2066, issues v OPS-170",
		},
		{
			name:  "no such issue, or no such team: nobody's, no error",
			keys:  []string{"HAL-1", "ZZZ-1"},
			want:  "HAL-1 -; ZZZ-1 -",
			asked: "teams a, teams v, issues a HAL-1",
		},
		{
			name:  "a bad key fails alone",
			keys:  []string{"nope", "HAL-2066"},
			want:  "nope - ! not an issue key; HAL-2066 HAL-2066/started@halcyon",
			asked: "teams a, teams v, issues a HAL-2066",
		},
		{
			name:  "the same key twice is asked once",
			keys:  []string{"HAL-2066", "hal-2066"},
			want:  "HAL-2066 HAL-2066/started@halcyon; hal-2066 HAL-2066/started@halcyon",
			asked: "teams a, teams v, issues a HAL-2066",
		},
		{
			name:  "signed out",
			setup: func(w *labelWorld) { w.index = workspaceIndex{} },
			keys:  []string{"HAL-2066"},
			want:  "HAL-2066 - ! " + errNotSignedIn.Error(),
		},
		{
			name:  "offline",
			setup: func(w *labelWorld) { w.down = true },
			keys:  []string{"HAL-2066"},
			want:  "HAL-2066 - ! halcyon: offline",
		},
		{
			name: "tick's team keys are used",
			setup: func(w *labelWorld) {
				writeLabelState(labelState{Teams: map[string]teamKeys{
					"a": {Fetched: w.now.Add(-time.Hour), Keys: []string{"HAL"}},
					"v": {Fetched: w.now.Add(-48 * time.Hour), Keys: []string{"OPS"}}, // too old
				}})
			},
			keys:  []string{"HAL-2066"},
			want:  "HAL-2066 HAL-2066/started@halcyon",
			asked: "teams v, issues a HAL-2066",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := lookupWorld(t)
			if c.setup != nil {
				c.setup(w)
			}
			rs, err := lookupIssues(context.Background(), config{}, c.keys, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := summary(rs); got != c.want {
				t.Errorf("got  %s\nwant %s", got, c.want)
			}
			if got := strings.Join(w.asked, ", "); got != c.asked {
				t.Errorf("asked %q, want %q", got, c.asked)
			}
		})
	}
}

func TestLookupIssuesCache(t *testing.T) {
	w := lookupWorld(t)
	ask := func(force bool, keys ...string) string {
		t.Helper()
		w.asked = nil
		rs, err := lookupIssues(context.Background(), config{}, keys, force)
		if err != nil {
			t.Fatal(err)
		}
		return summary(rs) + " | " + strings.Join(w.asked, ", ")
	}
	for _, step := range []struct {
		after time.Duration
		force bool
		keys  []string
		want  string
	}{
		{0, false, []string{"HAL-2066", "ZZZ-1"}, "HAL-2066 HAL-2066/started@halcyon; ZZZ-1 - | teams a, teams v, issues a HAL-2066"},
		// Within a minute: from the file, the nobody's key too.
		{30 * time.Second, false, []string{"HAL-2066", "ZZZ-1"}, "HAL-2066 HAL-2066/started@halcyon; ZZZ-1 - | "},
		// A key not asked before is, alone; the team keys are kept.
		{0, false, []string{"HAL-2066", "OPS-170"}, "HAL-2066 HAL-2066/started@halcyon; OPS-170 OPS-170/completed@halcyon-ops | issues v OPS-170"},
		// --force asks again, team keys included.
		{0, true, []string{"HAL-2066"}, "HAL-2066 HAL-2066/started@halcyon | teams a, teams v, issues a HAL-2066"},
		// A minute on, asked again.
		{time.Minute, false, []string{"HAL-2066"}, "HAL-2066 HAL-2066/started@halcyon | issues a HAL-2066"},
	} {
		w.now = w.now.Add(step.after)
		if got := ask(step.force, step.keys...); got != step.want {
			t.Errorf("after %v (force %v):\ngot  %s\nwant %s", step.after, step.force, got, step.want)
		}
	}

	// Offline once it's stale: the last answer, with why it's not fresher.
	w.now = w.now.Add(time.Minute)
	w.down = true
	if got, want := ask(false, "HAL-2066"), "HAL-2066 HAL-2066/started@halcyon ! halcyon: offline | "; got != want {
		t.Errorf("offline:\ngot  %s\nwant %s", got, want)
	}

	// A day on, an answer is dropped from the file.
	w.now = w.now.Add(25 * time.Hour)
	if _, err := lookupIssues(context.Background(), config{}, []string{"OPS-170"}, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := readLookupState().Issues["HAL-2066"]; ok {
		t.Error("a day-old answer is still kept")
	}
}

func TestRunIssuesPrints(t *testing.T) {
	lookupWorld(t)
	var out bytes.Buffer
	if err := runIssues(context.Background(), config{}, []string{`["HAL-2066","ZZZ-1","x"]`}, &out); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	want := `[{"issue":{"identifier":"HAL-2066","state":"In Progress","state_type":"started","title":"Cold open","url":"https://linear.app/halcyon/issue/HAL-2066","workspace":"halcyon"},"key":"HAL-2066"},{"issue":null,"key":"ZZZ-1"},{"error":"not an issue key","issue":null,"key":"x"}]`
	if b, _ := json.Marshal(got); string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}
