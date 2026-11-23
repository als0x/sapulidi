package guard

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/testutil"
)

func linkedWorktree(t *testing.T) (repo, wt string) {
	t.Helper()
	isolateGit(t)
	t.Setenv("SAPULIDI_TEST", "1")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	repo = testutil.MakeRepo(t, filepath.Join(realDir(t), "repo"))
	wt = filepath.Join(realDir(t), "wt")
	git(t, repo, "worktree", "add", "-b", "feat", wt)
	git(t, wt, "push", "-u", "origin", "feat")
	return repo, wt
}

const day = 24 * time.Hour

func TestWorktreeOrphanAdminGone(t *testing.T) {
	repo, wt := linkedWorktree(t)
	git(t, repo, "worktree", "list") // sanity
	adminDir := filepath.Join(repo, ".git", "worktrees")
	matches, _ := filepath.Glob(filepath.Join(adminDir, "*"))
	if len(matches) != 1 {
		t.Fatalf("admin dirs: %v", matches)
	}
	removeAll(t, matches[0])
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now().Add(7*day), day), "orphan")
	wantWhy(t, WorktreeBlocker(execx.Real{}, repo, time.Now().Add(7*day), day), "orphan") // .git is a directory
}

func TestWorktreeYoung(t *testing.T) {
	_, wt := linkedWorktree(t)
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now(), day), "young")
}

func TestWorktreeDirty(t *testing.T) {
	_, wt := linkedWorktree(t)
	write(t, filepath.Join(wt, "README"), "changed\n")
	write(t, filepath.Join(wt, "new.txt"), "x")
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now().Add(7*day), day), "dirty")
}

func TestWorktreeUntracked(t *testing.T) {
	_, wt := linkedWorktree(t)
	write(t, filepath.Join(wt, "new.txt"), "x")
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now().Add(7*day), day), "untracked")
}

func TestWorktreeStash(t *testing.T) {
	_, wt := linkedWorktree(t)
	write(t, filepath.Join(wt, "README"), "changed\n")
	git(t, wt, "stash")
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now().Add(7*day), day), "stash")
}

func TestWorktreeStashOfOtherBranchIgnored(t *testing.T) {
	repo, wt := linkedWorktree(t)
	write(t, filepath.Join(repo, "README"), "changed\n")
	git(t, repo, "stash") // on main
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now().Add(7*day), day), "")
}

func TestWorktreeUnpushed(t *testing.T) {
	_, wt := linkedWorktree(t)
	write(t, filepath.Join(wt, "f.txt"), "x")
	git(t, wt, "add", "f.txt")
	git(t, wt, "commit", "-m", "local")
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now().Add(7*day), day), "unpushed")
}

func TestWorktreeCleanPushedOldIsNil(t *testing.T) {
	_, wt := linkedWorktree(t)
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now().Add(7*day), day), "")
}

func TestWorktreeGitFailure(t *testing.T) {
	_, wt := linkedWorktree(t)
	wantWhy(t, WorktreeBlocker(failRunner{}, wt, time.Now().Add(7*day), day), "gitfail")
}

func TestWorktreeUntrackedDespiteConfig(t *testing.T) {
	repo, wt := linkedWorktree(t)
	git(t, repo, "config", "status.showUntrackedFiles", "no")
	write(t, filepath.Join(wt, "new.txt"), "x")
	wantWhy(t, WorktreeBlocker(execx.Real{}, wt, time.Now().Add(7*day), day), "untracked")
}
