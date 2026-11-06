package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The app mod pins the version it downloads; this build says it's that one.
func TestAppVersionIsThePlugins(t *testing.T) {
	toml, err := os.ReadFile("herdr-plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version = "([^"]+)"`).FindSubmatch(toml)
	if m == nil || string(m[1]) != appVersion {
		t.Fatalf("appVersion %q, herdr-plugin.toml %q", appVersion, m)
	}
	js, err := os.ReadFile("app/mods/linear/main.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `const LINEAR_VERSION = "`+appVersion+`"`) {
		t.Fatalf("app/mods/linear/main.js doesn't pin %s", appVersion)
	}
}

func appRun(t *testing.T, req string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := runApp(context.Background(), config{}, []string{req}, &out); err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	return res
}

// The demo's lists come grouped by status, and what's done to an issue stays
// done from one request to the next: a status change, a new issue.
func TestAppDemoKeepsWhatWasDone(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	mine := appRun(t, `{"op":"list","tab":"mine","demo":true}`)["issues"].([]any)
	first := mine[0].(map[string]any)
	if first["identifier"] != "HAL-212" || first["group"] != "In Review" || first["branch"] == "" {
		t.Fatalf("first is %v", first)
	}
	id := first["id"].(string)
	appRun(t, `{"op":"set-state","demo":true,"issue":"`+id+`","state":"s-progress"}`)
	d := appRun(t, `{"op":"detail","demo":true,"issue":"`+id+`"}`)
	if d["issue"].(map[string]any)["state"].(map[string]any)["name"] != "In Progress" || len(d["states"].([]any)) != 7 {
		t.Fatalf("after set-state: %v", d["issue"])
	}
	made := appRun(t, `{"op":"create","demo":true,"new":{"team":"t-hal","title":"Widgets show the last note","assignToMe":true}}`)
	key := made["issue"].(map[string]any)["identifier"].(string)
	recent := appRun(t, `{"op":"list","tab":"recent","demo":true}`)["issues"].([]any)
	if len(recent) == 0 || recent[0].(map[string]any)["identifier"] != key || recent[0].(map[string]any)["group"] != "" {
		t.Fatalf("recent: %v", recent)
	}
	found := appRun(t, `{"op":"find","demo":true,"key":"`+strings.ToLower(key)+`"}`)["issues"].([]any)
	if len(found) != 1 {
		t.Fatalf("find %s: %v", key, found)
	}
	if hits := appRun(t, `{"op":"search","demo":true,"query":"accents"}`)["issues"].([]any); len(hits) != 1 {
		t.Fatalf("search: %v", hits)
	}
	if res := appRun(t, `{"op":"create","demo":true,"new":{"team":"t-hal","title":"  "}}`); res["error"] == nil {
		t.Fatal("an issue without a title should be refused")
	}
}

// Start checks the issue before anything changes: never a coworker's.
func TestAppStartRefusesACoworkersIssue(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	src := newDemoSource()
	var theirs issue
	for _, is := range src.issues {
		if is.Assignee != nil && !is.Assignee.IsMe && isOpenState(is.State.Type) {
			theirs = is
			break
		}
	}
	if theirs.ID == "" {
		t.Skip("the demo has no coworker's open issue")
	}
	res := appRun(t, `{"op":"check-start","demo":true,"issue":"`+theirs.ID+`"}`)
	if !strings.Contains(res["error"].(string), "assigned to") {
		t.Fatalf("check-start: %v", res)
	}
	res = appRun(t, `{"op":"start","demo":true,"issue":"`+theirs.ID+`"}`)
	if res["error"] == nil {
		t.Fatal("start went ahead on a coworker's issue")
	}
}

// A job's answer is there once, when it's done; a job id is only ever one.
func TestAppJobAnswersOnce(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	id := "0123456789abcdef"
	_ = os.MkdirAll(filepath.Dir(jobPath(id)), 0o700)
	_ = os.WriteFile(jobPath(id), []byte(`{"done":false}`), 0o600)
	if res := appRun(t, `{"op":"job","job":"`+id+`"}`); res["done"] != false {
		t.Fatal(res)
	}
	_ = finishJob(id, map[string]any{"path": "/x"})
	if res := appRun(t, `{"op":"job","job":"`+id+`"}`); res["done"] != true {
		t.Fatal(res)
	}
	if res := appRun(t, `{"op":"job","job":"`+id+`"}`); res["error"] == nil {
		t.Fatal("a job's answer is given once")
	}
	if res := appRun(t, `{"op":"job","job":"../x"}`); res["error"] == nil {
		t.Fatal("a job id is only ever a job id")
	}
}

func shell(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The app's worktrees go where its own worktree mod puts them, on the
// issue's branch from the base; a second ask finds the first; outside a
// repo, it says how to give it one.
func TestAppWorktree(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.com")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	repo := filepath.Join(root, "notes")
	shell(t, root, "git", "init", "-q", "-b", "main", repo)
	shell(t, repo, "git", "commit", "-q", "--allow-empty", "-m", "init")
	ctx := context.Background()
	cfg := config{Base: "main", StartPrompt: "Work on {identifier}: {url}"}
	tr := appTree{Branch: "sam/hal-212-merge", TeamKey: "HAL", Identifier: "HAL-212", URL: "https://linear.app/halcyon/issue/HAL-212"}

	res, err := appWorktree(ctx, cfg, tr, []string{filepath.Join(repo)}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := res.(map[string]any)
	path := got["path"].(string)
	if want := filepath.Join(home, ".herdr", "worktrees", "notes", "sam-hal-212-merge"); path != want || got["created"] != true {
		t.Fatalf("made %v, want %s", got, want)
	}
	if b := shell(t, path, "git", "branch", "--show-current"); b != tr.Branch {
		t.Fatalf("on %q", b)
	}
	if got["prompt"] != "Work on HAL-212: https://linear.app/halcyon/issue/HAL-212" {
		t.Fatalf("prompt %q", got["prompt"])
	}
	again, err := appWorktree(ctx, cfg, tr, []string{repo}, "")
	if err != nil || again.(map[string]any)["created"] != false || again.(map[string]any)["path"] != path {
		t.Fatalf("again: %v %v", again, err)
	}
	// The team's mapped repo wins over where you are.
	cfg.Repos = map[string]string{"halcyon/HAL": repo}
	tr2 := tr
	tr2.Branch, tr2.WorkspaceKey = "sam/hal-213-other", "halcyon"
	if _, err := appWorktree(ctx, cfg, tr2, []string{root}, ""); err != nil {
		t.Fatalf("mapped repo: %v", err)
	}
	// Nowhere to make it.
	cfg.Repos = nil
	_, err = appWorktree(ctx, cfg, appTree{Branch: "x", TeamKey: "OPS"}, []string{root}, "")
	if e := appError(err); e["noRepo"] != true {
		t.Fatalf("no repo: %v", e)
	}
	if err := removeAppWorktree(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := removeAppWorktree(ctx, repo); err == nil {
		t.Fatal("removed the repo's own checkout")
	}
}
