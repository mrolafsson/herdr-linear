package main

import (
	"strings"
	"testing"
)

func TestIssueTargetUsesLinearBranchName(t *testing.T) {
	is := mkIssue("ACT-42", "started", "In Progress", 0, 0)
	is.BranchName = "hjortur/act-42-fix-the-thing"
	is.Team.Key = "ACT"
	got := issueTarget(is)
	if got.Branch != "hjortur/act-42-fix-the-thing" || got.TeamKey != "ACT" || !strings.HasPrefix(got.Label, "ACT-42 ") {
		t.Fatalf("%+v", got)
	}
	is.BranchName = ""
	if got := issueTarget(is); got.Branch != "act-42" {
		t.Fatalf("fallback branch %q", got.Branch)
	}
}

func TestProjectTargetUsesURLSlug(t *testing.T) {
	p := project{Name: "Daily brief", SlugID: "a1b2", URL: "https://linear.app/action/project/daily-brief-a1b2/"}
	p.Teams.Nodes = append(p.Teams.Nodes, teamKey{"ACT"})
	got := projectTarget(p)
	if got.Branch != "project/daily-brief-a1b2" || got.TeamKey != "ACT" {
		t.Fatalf("%+v", got)
	}
	p.URL = ""
	if got := projectTarget(p); got.Branch != "project/a1b2" {
		t.Fatalf("fallback %q", got.Branch)
	}
}

func TestShortenCountsRunes(t *testing.T) {
	if got := shorten("ÆÐÞ öll", 4); got != "ÆÐÞ…" {
		t.Fatalf("%q", got)
	}
	if got := shorten("short", 10); got != "short" {
		t.Fatalf("%q", got)
	}
}

func TestRepoForPrefersConfiguredTeamRepo(t *testing.T) {
	cfg := config{Repos: map[string]string{"ACT": "/repos/web-app"}}
	if got, _ := repoFor(cfg, "ACT", "/somewhere/else"); got != "/repos/web-app" {
		t.Fatal(got)
	}
	if got, _ := repoFor(cfg, "OPS", "/invoked"); got != "/invoked" {
		t.Fatal(got)
	}
	if _, err := repoFor(cfg, "OPS", ""); err == nil {
		t.Fatal("want an error with nowhere to make the worktree")
	}
}

func TestExpandPrompt(t *testing.T) {
	is := mkIssue("ACT-7", "started", "", 0, 0)
	is.URL = "https://linear.app/x/issue/ACT-7"
	if got := expandPrompt("/ticket {identifier} {url}", is); got != "/ticket ACT-7 https://linear.app/x/issue/ACT-7" {
		t.Fatal(got)
	}
}

func TestPickAgentPanePrefersRootPane(t *testing.T) {
	panes := []paneInfo{{PaneID: "w1:p1"}, {PaneID: "w1:p2", Agent: "claude"}, {PaneID: "w1:p3", Agent: "claude"}}
	if got := pickAgentPane(panes, "w1:p3"); got == nil || got.PaneID != "w1:p3" {
		t.Fatalf("%v", got)
	}
	if got := pickAgentPane(panes, "w1:p1"); got == nil || got.PaneID != "w1:p2" {
		t.Fatalf("root has no agent: want first agent pane, got %v", got)
	}
	if got := pickAgentPane(panes[:1], "w1:p1"); got != nil {
		t.Fatalf("no agent anywhere: got %v", got)
	}
}

func TestConfigDefaults(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != defaultClientID || cfg.StartPrompt != "/ticket {identifier}" || cfg.AgentWaitSeconds != 90 {
		t.Fatalf("%+v", cfg)
	}
}
