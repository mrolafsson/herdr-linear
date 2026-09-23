package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"
	"time"
)

// target is one thing a worktree can be made for: an issue or a project.
type target struct {
	Branch  string
	Label   string
	TeamKey string
}

func issueTarget(is issue) target {
	branch := is.BranchName
	if branch == "" {
		branch = strings.ToLower(is.Identifier)
	}
	return target{Branch: branch, Label: shorten(is.Identifier+" "+is.Title, 48), TeamKey: is.Team.Key}
}

func projectTarget(p project) target {
	// The URL's last segment is Linear's own slug ("name-slug-a1b2c3"): stable
	// and unique, unlike the display name.
	slug := path.Base(strings.TrimRight(p.URL, "/"))
	if slug == "" || slug == "." || slug == "/" {
		slug = p.SlugID
	}
	t := target{Branch: "project/" + slug, Label: shorten(p.Name, 48)}
	if len(p.Teams.Nodes) > 0 {
		t.TeamKey = p.Teams.Nodes[0].Key
	}
	return t
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// repoFor picks the checkout a worktree is made from: the team's configured
// repo, else the repo of the space the picker was opened from.
func repoFor(cfg config, teamKey, invokedCwd string) (string, error) {
	if dir, ok := cfg.Repos[teamKey]; ok && dir != "" {
		return dir, nil
	}
	if invokedCwd != "" {
		return invokedCwd, nil
	}
	return "", fmt.Errorf("no repo for team %s: open the picker from a space inside the repo, or map it under \"repos\" in %s/config.json", teamKey, configDir())
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// baseRef resolves what a new branch starts from and fetches it first, so a
// worktree never starts from a stale main. Offline, the local copy is used.
func baseRef(ctx context.Context, cfg config, repo string) string {
	base := cfg.Base
	if base == "" {
		if ref, err := git(ctx, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
			base = ref
		} else {
			base = "origin/main"
		}
	}
	if remote, branch, ok := strings.Cut(base, "/"); ok {
		fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		_, _ = git(fctx, repo, "fetch", "--quiet", remote, branch)
	}
	return base
}

// openWorktree focuses the target's worktree, creating it when there is none.
// It returns the workspace and, for a new worktree, its root pane.
func openWorktree(ctx context.Context, cfg config, t target, repo string) (*worktreeResult, bool, error) {
	existing, err := listWorktrees(repo)
	if err != nil {
		return nil, false, err
	}
	for _, w := range existing {
		if strings.TrimPrefix(w.Branch, "refs/heads/") == t.Branch {
			var res worktreeResult
			err := herdrCall("worktree.open", map[string]any{"cwd": repo, "path": w.Path, "label": t.Label, "focus": true}, &res)
			return &res, false, err
		}
	}

	params := map[string]any{"cwd": repo, "branch": t.Branch, "label": t.Label, "focus": true}
	// A branch that already exists (its worktree was removed) is checked out
	// as it is; only a new branch needs a base.
	if _, err := git(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+t.Branch); err != nil {
		params["base"] = baseRef(ctx, cfg, repo)
	}
	var res worktreeResult
	if err := herdrCall("worktree.create", params, &res); err != nil {
		return nil, false, err
	}
	return &res, true, nil
}

// ── kickoff: hand the new worktree's agent its first prompt ───────────────────

// spawnKickoff runs `kickoff` detached, so it outlives the popup that started it.
func spawnKickoff(workspaceID, rootPaneID, text string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(stateDir()+"/kickoff.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(self, "kickoff", workspaceID, rootPaneID, text)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// kickoff waits for an agent to come up in the workspace (your worktree
// template starts it), for it to be ready for input, then sends the prompt.
// An approval or trust dialog (blocked) is waited out: that's yours to answer.
func kickoff(cfg config, workspaceID, rootPaneID, text string) error {
	deadline := time.Now().Add(time.Duration(cfg.AgentWaitSeconds) * time.Second)
	for time.Now().Before(deadline) {
		panes, err := listPanes(workspaceID)
		if err != nil {
			return err
		}
		if p := pickAgentPane(panes, rootPaneID); p != nil && (p.AgentStatus == "idle" || p.AgentStatus == "done") {
			err := herdrCall("agent.prompt", map[string]any{
				"target": p.PaneID, "text": text,
				"wait": map[string]any{"timeout_ms": 8000, "until": []string{"working", "blocked"}},
			}, nil)
			switch {
			case err == nil:
				fmt.Println(time.Now().Format(time.RFC3339), "sent", text, "to", p.PaneID)
				return nil
			case isHerdrCode(err, "agent_prompt_stalled"), isHerdrCode(err, "agent_blocked"), isHerdrCode(err, "timeout"):
				// Not ready after all (still booting, or a dialog came up).
				// A stalled prompt may still be sitting in the input box, so
				// resending could duplicate it: stop here and say so.
				if isHerdrCode(err, "agent_prompt_stalled") || isHerdrCode(err, "timeout") {
					notify("Linear", "Typed \""+text+"\" but the agent didn't start. Press enter in the new space.")
					return err
				}
			default:
				return err
			}
		}
		time.Sleep(time.Second)
	}
	notify("Linear", "No agent came up in the new space; run \""+text+"\" yourself.")
	return errors.New("no ready agent before the deadline")
}

func pickAgentPane(panes []paneInfo, rootPaneID string) *paneInfo {
	var first *paneInfo
	for i := range panes {
		p := &panes[i]
		if p.Agent == "" {
			continue
		}
		if p.PaneID == rootPaneID {
			return p
		}
		if first == nil {
			first = p
		}
	}
	return first
}

func expandPrompt(tmpl string, is issue) string {
	return strings.NewReplacer("{identifier}", is.Identifier, "{title}", is.Title, "{url}", is.URL).Replace(tmpl)
}
