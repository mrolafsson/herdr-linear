package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppWorktreeCommandMakesTheWorktree(t *testing.T) {
	root := exactRepoFixture(t)
	repo := initExactRepo(t, root, "app")
	custom := filepath.Join(root, "app.worktrees", "hal-212")
	seen := filepath.Join(root, "seen")
	t.Setenv("SEEN", seen)
	t.Setenv("TARGET", custom)
	cfg := config{Base: "main", WorktreeCommand: `env | grep '^HERDR_LINEAR_' | sort > "$SEEN" && git worktree add -q -b "$HERDR_LINEAR_BRANCH" "$TARGET" "$HERDR_LINEAR_BASE"`}
	branch := "sam/hal-212-custom"

	got := exactRepoRequest(t, cfg, repo, nil, branch)
	if got["error"] != nil || got["path"] != custom || got["created"] != true {
		t.Fatalf("command: %v; want %q", got, custom)
	}
	env, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	want := "HERDR_LINEAR_BASE=main\nHERDR_LINEAR_BRANCH=" + branch + "\nHERDR_LINEAR_LABEL=\nHERDR_LINEAR_REPO=" + repo + "\n"
	if string(env) != want {
		t.Fatalf("environment:\n%s\nwant:\n%s", env, want)
	}
	if _, err := os.Stat(worktreeRoot()); !os.IsNotExist(err) {
		t.Fatalf("also made a worktree in the default place: %v", err)
	}

	// Opened again, the worktree is found by its branch; the command doesn't run.
	if err := os.Remove(seen); err != nil {
		t.Fatal(err)
	}
	again := exactRepoRequest(t, cfg, repo, nil, branch)
	if again["error"] != nil || again["path"] != custom || again["created"] != false {
		t.Fatalf("reopen: %v", again)
	}
	if _, err := os.Stat(seen); !os.IsNotExist(err) {
		t.Fatal("ran the command for a worktree that already exists")
	}
}

func TestAppWorktreeCommandGetsNoBaseForAnExistingBranch(t *testing.T) {
	root := exactRepoFixture(t)
	repo := initExactRepo(t, root, "app")
	shell(t, repo, "git", "branch", "sam/existing")
	custom := filepath.Join(root, "existing")
	seen := filepath.Join(root, "base")
	t.Setenv("SEEN", seen)
	t.Setenv("TARGET", custom)
	cfg := config{Base: "main", WorktreeCommand: `printf '%s' "$HERDR_LINEAR_BASE" > "$SEEN" && git worktree add -q "$TARGET" "$HERDR_LINEAR_BRANCH"`}

	got := exactRepoRequest(t, cfg, repo, nil, "sam/existing")
	if got["error"] != nil || got["path"] != custom {
		t.Fatalf("existing branch: %v; want %q", got, custom)
	}
	if base, err := os.ReadFile(seen); err != nil || len(base) != 0 {
		t.Fatalf("base for an existing branch: %q, %v; want none", base, err)
	}
}

func TestWorktreeCommandRunsAtTheRepoTop(t *testing.T) {
	root := exactRepoFixture(t)
	repo := initExactRepo(t, root, "app")
	sub := filepath.Join(repo, "web", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(root, "seen")
	t.Setenv("SEEN", seen)
	cfg := config{Base: "main", WorktreeCommand: `printf '%s\n%s' "$HERDR_LINEAR_REPO" "$PWD" > "$SEEN" && git worktree add -q -b "$HERDR_LINEAR_BRANCH" ../wt "$HERDR_LINEAR_BASE"`}

	path, err := worktreeByCommand(context.Background(), cfg, sub, target{Branch: "sam/from-sub"})
	if err != nil || path != filepath.Join(root, "wt") {
		t.Fatalf("from a subfolder: %q, %v", path, err)
	}
	if got, _ := os.ReadFile(seen); string(got) != repo+"\n"+repo {
		t.Fatalf("repo and working directory: %q; want %q twice", got, repo)
	}
}

func TestAppWorktreeCommandErrorsCarryItsOutput(t *testing.T) {
	for _, c := range []struct{ command, want string }{
		{`echo 'no space left on device' >&2; exit 3`, "no space left on device"},
		{`echo 'made nothing'`, "no worktree"},
		{`echo 'made nothing'`, "made nothing"},
	} {
		t.Run(c.want, func(t *testing.T) {
			root := exactRepoFixture(t)
			repo := initExactRepo(t, root, "app")
			got := exactRepoRequest(t, config{Base: "main", WorktreeCommand: c.command}, repo, nil, "sam/broken")
			msg, _ := got["error"].(string)
			if !strings.Contains(msg, c.want) {
				t.Fatalf("error %q; want it to mention %q", msg, c.want)
			}
		})
	}
}
