package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
)

// nestedClone clones a fresh local bare "remote" (one pushed commit on main) into dir/rel and returns the clone.
func nestedClone(t *testing.T, dir, rel string) string {
	t.Helper()
	remote := filepath.Join(realDir(t), "remote.git")
	git(t, realDir(t), "init", "--bare", "-q", remote)
	seed := filepath.Join(realDir(t), "seed")
	git(t, realDir(t), "init", "-q", seed)
	write(t, filepath.Join(seed, "a.txt"), "a\n")
	git(t, seed, "add", "a.txt")
	nestedCommit(t, seed, "seed")
	git(t, seed, "branch", "-M", "main")
	git(t, seed, "push", "-q", remote, "main")
	clone := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "clone", "-q", "-b", "main", remote, clone)
	return clone
}

func nestedCommit(t *testing.T, repo, msg string) {
	t.Helper()
	git(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", msg)
}

func wantNested(t *testing.T, err error, reason string) {
	t.Helper()
	wantWhy(t, err, "nested-git")
	if d := err.(*core.KeepError).Detail; !strings.Contains(d, reason) {
		t.Fatalf("detail %q does not name %q", d, reason)
	}
}

func TestNestedCleanPushedCloneIsRemovable(t *testing.T) {
	_, dir := projectRepo(t)
	nestedClone(t, dir, "out/src")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "")
}

func TestNestedUnsafeClonesAreKept(t *testing.T) {
	cases := []struct {
		name, reason string
		mutate       func(t *testing.T, clone string)
	}{
		{"modified", "dirty", func(t *testing.T, c string) { write(t, filepath.Join(c, "a.txt"), "changed\n") }},
		{"untracked", "untracked", func(t *testing.T, c string) { write(t, filepath.Join(c, "new.txt"), "n\n") }},
		{"stash", "stash", func(t *testing.T, c string) {
			write(t, filepath.Join(c, "a.txt"), "changed\n")
			git(t, c, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "stash", "-q")
		}},
		{"unpushed", "unpushed", func(t *testing.T, c string) {
			write(t, filepath.Join(c, "b.txt"), "b\n")
			git(t, c, "add", "b.txt")
			nestedCommit(t, c, "local")
		}},
		{"detached", "unpushed", func(t *testing.T, c string) {
			git(t, c, "checkout", "-q", "--detach")
			write(t, filepath.Join(c, "b.txt"), "b\n")
			git(t, c, "add", "b.txt")
			nestedCommit(t, c, "local")
		}},
		{"no-remote", "no-remote", func(t *testing.T, c string) { git(t, c, "remote", "remove", "origin") }},
		{"bare", "bare", func(t *testing.T, c string) {
			if err := os.RemoveAll(c); err != nil {
				t.Fatal(err)
			}
			git(t, filepath.Dir(c), "init", "--bare", "-q", c)
		}},
		{"gitfile", "gitfile", func(t *testing.T, c string) {
			if err := os.RemoveAll(filepath.Join(c, ".git")); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(c, ".git"), "gitdir: /nonexistent\n")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, dir := projectRepo(t)
			clone := nestedClone(t, dir, "out/src")
			tc.mutate(t, clone)
			wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), tc.reason)
		})
	}
}

func TestNestedNamesTheFailingRepository(t *testing.T) {
	_, dir := projectRepo(t)
	nestedClone(t, dir, "a/safe")
	dirty := nestedClone(t, dir, "b/dirty")
	write(t, filepath.Join(dirty, "x.txt"), "x\n")
	err := ProjectArtifact(execx.Real{}, dir, markers)
	wantNested(t, err, "dirty")
	if d := err.(*core.KeepError).Detail; !strings.Contains(d, filepath.Join("b", "dirty")) || strings.Contains(d, "safe") {
		t.Fatalf("detail %q should name only b/dirty", d)
	}
}

func TestNestedMoreThanSixteenIsKept(t *testing.T) {
	_, dir := projectRepo(t)
	for i := range 17 {
		write(t, filepath.Join(dir, fmt.Sprintf("r%02d", i), ".git", "HEAD"), "ref: x")
	}
	wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "more than 16 nested repositories")
}

func TestNestedProofRunsNoFsmonitor(t *testing.T) {
	_, dir := projectRepo(t)
	clone := nestedClone(t, dir, "out/src")
	marker := trapFsmonitor(t, clone, clone)
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the nested proof ran the nested repository's core.fsmonitor command")
	}
}

func TestNestedGainingWorkBetweenScanAndProofKeeps(t *testing.T) {
	_, dir := projectRepo(t)
	clone := nestedClone(t, dir, "out/src")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "")
	write(t, filepath.Join(clone, "a.txt"), "edited after the scan\n")
	wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "dirty")
}
