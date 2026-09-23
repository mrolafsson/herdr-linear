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

	"github.com/charmbracelet/x/ansi"
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
	// A project spanning several teams has no single repo: guessing one could
	// put the worktree in the wrong repository. Only a one-team project uses
	// its team's mapping; otherwise it's the repo you opened the picker from.
	if len(p.Teams.Nodes) == 1 {
		t.TeamKey = p.Teams.Nodes[0].Key
	}
	return t
}

// shorten fits s into n terminal cells, not n characters: a CJK character or
// an emoji takes two cells.
func shorten(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return strings.TrimRight(ansi.Truncate(s, n-1, ""), " ") + "…"
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
	if teamKey == "" {
		return "", errors.New("this project spans several teams, so it has no one repo: open the picker from a space inside the repo you want")
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

// Seams for tests: the herdr side of doWorktree.
var (
	openWorktreeFn = openWorktree
	spawnKickoffFn = spawnKickoff
)

// doWorktree opens (or creates) t's worktree; with start, it also starts the
// issue and hands the new worktree's agent its prompt. Start spans Linear,
// git and an agent, with no transaction across them, so the order is chosen
// so a failure leaves nothing half-done that you'd have to notice:
//
//  1. Re-read the issue. If it's closed or someone else's, stop: nothing
//     has been changed. The worktree is made from this fresh copy (its
//     branch or team may have changed since the popup loaded).
//  2. The worktree. If it fails, Linear is still untouched.
//  3. Re-read again: making the worktree can take a while (a fetch, the
//     template). Then Linear: In Progress, and yours if unowned. Linear has
//     no compare-and-set, so a change in the moment between this read and
//     the update can still be overwritten; the window is now small.
//  4. The prompt, only for a worktree created just now, only to its own agent.
func doWorktree(ctx context.Context, cfg config, client source, invoked string, t target, is *issue, start bool) actionDoneMsg {
	start = start && is != nil
	if start {
		fresh, err := client.freshIssue(ctx, is.ID)
		if err != nil {
			return actionDoneMsg{err: fmt.Errorf("couldn't check %s before starting it: %w", is.Identifier, err)}
		}
		if err := startConflict(fresh); err != nil {
			return actionDoneMsg{err: err}
		}
		t = issueTarget(fresh)
	}

	repo, err := repoFor(cfg, t.TeamKey, invoked)
	if err != nil {
		return actionDoneMsg{err: err}
	}
	res, created, err := openWorktreeFn(ctx, cfg, t, repo)
	if err != nil {
		return actionDoneMsg{err: err}
	}
	if !start {
		return actionDoneMsg{}
	}

	fresh, err := client.freshIssue(ctx, is.ID)
	if err != nil {
		return actionDoneMsg{note: "The worktree is ready, but " + is.Identifier + " couldn't be checked again (" + err.Error() + "), so it wasn't started."}
	}
	if err := startConflict(fresh); err != nil {
		return actionDoneMsg{note: "The worktree is ready, but it changed meanwhile: " + err.Error() + "."}
	}
	// The worktree was made from the first read. If the branch or team moved
	// since, the prompt would set the agent on the issue in the wrong place.
	if now := issueTarget(fresh); now.Branch != t.Branch || now.TeamKey != t.TeamKey {
		return actionDoneMsg{note: "The worktree is ready, but " + is.Identifier + "'s branch or team changed meanwhile, so it wasn't started. Open it again to get the right worktree."}
	}
	if err := client.startIssue(ctx, fresh); err != nil {
		return actionDoneMsg{note: "The worktree is ready, but Linear wasn't updated (" + err.Error() + "), so no prompt was sent."}
	}
	if !created {
		// Its agent may be mid-task: don't type into it.
		return actionDoneMsg{note: is.Identifier + " is in progress. Its worktree already existed, so no prompt was sent."}
	}
	if res.RootPane == nil || res.RootPane.PaneID == "" {
		return actionDoneMsg{note: is.Identifier + " is in progress, but herdr didn't say which pane is the new worktree's, so no prompt was sent."}
	}
	if err := spawnKickoffFn(res.Workspace.WorkspaceID, res.RootPane.PaneID, expandPrompt(cfg.StartPrompt, fresh)); err != nil {
		return actionDoneMsg{note: "Worktree created, but the prompt couldn't be queued: " + err.Error()}
	}
	return actionDoneMsg{}
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
	logPath := stateDir() + "/kickoff.log"
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 256<<10 {
		_ = os.Rename(logPath, logPath+".old") // keep the log small: one generation back
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	// The prompt goes over stdin, not argv: any local user can list another
	// process's arguments, and the prompt carries issue text.
	cmd := exec.Command(self, "kickoff", workspaceID, rootPaneID)
	cmd.Stdin = strings.NewReader(text)
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
		if p := agentPane(panes, rootPaneID); p != nil && (p.AgentStatus == "idle" || p.AgentStatus == "done") {
			err := herdrCall("agent.prompt", map[string]any{
				"target": p.PaneID, "text": text,
				"wait": map[string]any{"timeout_ms": 8000, "until": []string{"working", "blocked"}},
			}, nil)
			switch {
			case err == nil:
				// The log records that it happened, not the issue text.
				fmt.Println(time.Now().Format(time.RFC3339), "prompt sent to", p.PaneID)
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

// agentPane is the new worktree's own pane, once an agent runs in it. Only
// that pane: another agent in the space may be busy with something else, and
// the prompt can set it off on autonomous work.
func agentPane(panes []paneInfo, rootPaneID string) *paneInfo {
	for i := range panes {
		if p := &panes[i]; p.PaneID == rootPaneID && p.Agent != "" {
			return p
		}
	}
	return nil
}

func expandPrompt(tmpl string, is issue) string {
	return strings.NewReplacer("{identifier}", is.Identifier, "{title}", is.Title, "{url}", is.URL).Replace(tmpl)
}
