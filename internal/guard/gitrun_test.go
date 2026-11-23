package guard

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/execx"
)

// trapFsmonitor sets core.fsmonitor in repo's own config to a script that writes a marker, and returns the marker.
// It first proves with a plain git status that the trap fires, so a quiet marker later means something.
func trapFsmonitor(t *testing.T, repo, statusDir string) string {
	t.Helper()
	marker := filepath.Join(realDir(t), "fsmonitor-ran")
	script := filepath.Join(realDir(t), "fsmonitor.sh")
	write(t, script, "#!/bin/sh\ntouch '"+marker+"'\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "config", "core.fsmonitor", script)
	git(t, statusDir, "status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not run core.fsmonitor on status; the trap cannot be armed")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	return marker
}

func TestGitHardeningWorktreeBlockerRunsNoFsmonitor(t *testing.T) {
	repo, wt := linkedWorktree(t)
	marker := trapFsmonitor(t, repo, wt)
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now().Add(7*day), day), "")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("WorktreeBlocker ran the repository's core.fsmonitor command")
	}
}

func TestGitHardeningProjectArtifactRunsNoFsmonitor(t *testing.T) {
	repo, dir := projectRepo(t)
	marker := trapFsmonitor(t, repo, repo)
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ProjectArtifact ran the repository's core.fsmonitor command")
	}
}

func TestHoldsRepoStopsOnCancelledContext(t *testing.T) {
	d := realDir(t)
	write(t, filepath.Join(d, "a", "b", "file"), "x")
	ctx, cancel := context.WithCancel(context.Background())
	if held, err := HoldsRepo(ctx, d); held || err != nil {
		t.Fatalf("live context: %v %v", held, err)
	}
	cancel()
	if _, err := HoldsRepo(ctx, d); err == nil {
		t.Fatal("a cancelled walk must return its error so the entry is kept")
	}
	write(t, filepath.Join(d, "r", ".git", "HEAD"), "x")
	if held, err := HoldsRepo(context.Background(), d); !held || err != nil {
		t.Fatalf("repository not found: %v %v", held, err)
	}
}

type recRunner struct {
	inner execx.Real
	calls [][]string
}

func (r *recRunner) Run(ctx context.Context, dir string, argv ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), argv...))
	return r.inner.Run(ctx, dir, argv...)
}

func (r *recRunner) LookPath(name string) (string, error) { return r.inner.LookPath(name) }

// The worktree guard's status must not take optional locks: a status run takes the index lock to refresh it,
// which could collide with the user's own git while a scheduled run looks at their worktree.
func TestWorktreeStatusTakesNoOptionalLocks(t *testing.T) {
	_, wt := linkedWorktree(t)
	r := &recRunner{}
	wantWhy(t, WorktreeBlocker(r, wt, time.Now().Add(7*day), day), "")
	found := false
	for _, c := range r.calls {
		if slices.Contains(c, "status") {
			found = true
			s := strings.Join(c, " ")
			if !strings.Contains(s, "--no-optional-locks status") {
				t.Errorf("status without --no-optional-locks: %v", c)
			}
		}
	}
	if !found {
		t.Fatal("no status call recorded")
	}
}

func TestGitArgvPrefix(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")
	got := strings.Join(GitArgv("/r", "status", "--porcelain"), " ")
	want := "git -c core.fsmonitor=false -c core.hooksPath=/dev/null -c core.pager=cat -C /r status --porcelain"
	if got != want {
		t.Fatalf("argv\n got %s\nwant %s", got, want)
	}
	// Building an argv has no process-wide side effect: the runner sets GIT_CONFIG_NOSYSTEM for its git children.
	if v, ok := os.LookupEnv("GIT_CONFIG_NOSYSTEM"); !ok || v != "" {
		t.Fatalf("GitArgv changed the process environment: %q set=%v", v, ok)
	}
}
