package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func exactRepoFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.invalid")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	return root
}

func initExactRepo(t *testing.T, root, name string) string {
	t.Helper()
	repo := filepath.Join(root, name)
	shell(t, root, "git", "init", "-q", "-b", "main", repo)
	shell(t, repo, "git", "commit", "-q", "--allow-empty", "-m", "init")
	return repo
}

// Exercise the JSON boundary used by the mod, without Linear, credentials or fetch.
func exactRepoRequest(t *testing.T, cfg config, repo string, dirs []string, branch string) map[string]any {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"op": "worktree", "repo": repo, "dirs": dirs,
		"target": appTree{Branch: branch, TeamKey: "HAL", WorkspaceKey: "halcyon", Identifier: "HAL-212"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runApp(context.Background(), cfg, []string{string(data)}, &out); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func exactGitPath(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(out), "\n")
}

func TestAppExplicitRepoPreservesPaths(t *testing.T) {
	for _, name := range []string{"notes ", "notes\t", "notes\n", "notes\r", "line\nbreak", "café ' $literal"} {
		t.Run(name, func(t *testing.T) {
			root := exactRepoFixture(t)
			sibling := initExactRepo(t, root, "notes")
			repo := initExactRepo(t, root, name)
			cfg := config{Base: "main", Repos: map[string]string{"HAL": sibling, "halcyon/HAL": sibling, "": sibling}}
			branch := "sam/hal-212-exact"
			for i := 0; i < 2; i++ {
				got := exactRepoRequest(t, cfg, repo, []string{sibling}, branch)
				want := filepath.Join(worktreeRoot(), name, slug(branch))
				if got["error"] != nil || got["path"] != want || got["created"] != (i == 0) {
					t.Fatalf("request %d: %v; want %q", i, got, want)
				}
				if common := exactGitPath(t, want, "rev-parse", "--path-format=absolute", "--git-common-dir"); common != filepath.Join(repo, ".git") {
					t.Fatalf("wrong repository: %q", common)
				}
			}
			if _, err := git(context.Background(), sibling, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
				t.Fatal("created the branch in the sibling repository")
			}
			// Removal uses the same exact common-directory path; it must never remove the base.
			if err := removeAppWorktree(context.Background(), repo); err == nil {
				t.Fatal("removed the base repository")
			}
			if err := removeAppWorktree(context.Background(), filepath.Join(worktreeRoot(), name, slug(branch))); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAppRepoDirsPreservePaths(t *testing.T) {
	for _, name := range []string{"notes ", "notes\n", "line\nbreak"} {
		t.Run(name, func(t *testing.T) {
			root := exactRepoFixture(t)
			initExactRepo(t, root, "notes")
			repo := initExactRepo(t, root, name)
			got := exactRepoRequest(t, config{Base: "main"}, "", []string{repo}, "sam/from-dirs")
			if got["error"] != nil || got["path"] != filepath.Join(worktreeRoot(), name, "sam-from-dirs") {
				t.Fatalf("dirs: %v", got)
			}
		})
	}
}

func TestAppExplicitRepoAcceptsLinkedWorktree(t *testing.T) {
	root := exactRepoFixture(t)
	repo := initExactRepo(t, root, "base ")
	linked := filepath.Join(root, "linked\ncheckout ")
	shell(t, repo, "git", "worktree", "add", "-q", "-b", "linked", linked)
	got := exactRepoRequest(t, config{Base: "main"}, linked, nil, "sam/from-linked")
	want := filepath.Join(worktreeRoot(), "base ", "sam-from-linked")
	if got["error"] != nil || got["path"] != want {
		t.Fatalf("linked .git file: %v; want %q", got, want)
	}
	again := exactRepoRequest(t, config{Base: "main"}, linked, nil, "sam/from-linked")
	if again["path"] != want || again["created"] != false {
		t.Fatalf("reopen linked: %v", again)
	}
}

func TestAppExplicitRepoFailsClosed(t *testing.T) {
	root := exactRepoFixture(t)
	repo := initExactRepo(t, root, "valid")
	nested := filepath.Join(repo, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "bare")
	shell(t, root, "git", "init", "--bare", "-q", bare)
	for _, invalid := range []string{"relative", filepath.Join(root, "missing"), repo + "\x00bad", root, nested, bare} {
		got := exactRepoRequest(t, config{Base: "main", Repos: map[string]string{"HAL": repo}}, invalid, []string{repo}, "sam/must-not-exist")
		if got["error"] == nil {
			t.Fatalf("accepted invalid override %q: %v", invalid, got)
		}
	}
	if _, err := os.Stat(worktreeRoot()); !os.IsNotExist(err) {
		t.Fatalf("invalid override created worktree directories: %v", err)
	}
}

func TestAppExistingWorktreePathWithNewlines(t *testing.T) {
	root := exactRepoFixture(t)
	repo := initExactRepo(t, root, "base")
	linked := filepath.Join(root, "existing\n\nbranch\nfake ")
	shell(t, repo, "git", "worktree", "add", "-q", "-b", "sam/existing", linked)
	got := exactRepoRequest(t, config{Base: "main"}, repo, []string{repo}, "sam/existing")
	if got["error"] != nil || got["path"] != linked || got["created"] != false {
		t.Fatalf("existing: %v; want %q", got, linked)
	}
	// The main checkout is still excluded, even when its branch matches.
	if got := gitWorktreeOn(context.Background(), repo, "main"); got != "" {
		t.Fatalf("reused main checkout: %q", got)
	}
}
