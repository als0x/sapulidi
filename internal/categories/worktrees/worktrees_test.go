package worktrees

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/guard"
	"github.com/als0x/sapulidi/internal/testutil"
)

// armFsmonitor sets core.fsmonitor in repo's config to a script that writes a marker, and returns the marker.
func armFsmonitor(t *testing.T, repo string) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	script := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "config", "core.fsmonitor", script)
	return marker
}

// Every git call of the worktrees categories (worktree list, the guard's status, worktree prune) is hardened.
func TestWorktreesGitCallsAreHardened(t *testing.T) {
	f := newFixture(t)
	old, gone := f.wtPath("old"), f.wtPath("gone")
	f.addWorktree(t, old, "old")
	f.addWorktree(t, gone, "gone")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	marker := armFsmonitor(t, f.repo) // after the setup: the test's own unhardened git must not fire it
	f.planWorktrees()
	cands, _, err := (&pruneCat{}).Scan(f.env)
	if err != nil || len(cands) != 1 {
		t.Fatalf("prune candidates %v %v", cands, err)
	}
	if err := (&pruneCat{}).Proof(f.env, cands[0]); err != nil {
		t.Fatalf("prune proof: %v", err)
	}
	_ = (&worktreesCat{}).Proof(f.env, core.Candidate{RuleKey: "worktrees/linked@1", Path: old, Dir: f.repo}) // runs the status guard
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a git call of the worktrees categories ran the repository's core.fsmonitor command")
	}
	calls := 0
	for _, c := range f.env.Run.(*testutil.FakeRunner).Calls {
		if len(c.Argv) > 0 && c.Argv[0] == "git" {
			calls++
			if !strings.Contains(strings.Join(c.Argv, " "), "-c core.fsmonitor=false -c core.hooksPath=/dev/null -c core.pager=cat") {
				t.Errorf("unhardened git call: %v", c.Argv)
			}
		}
	}
	if calls == 0 {
		t.Fatal("no git call was recorded")
	}
}

func testRules() []core.Rule {
	return []core.Rule{
		{Pack: "worktrees", ID: "linked", Rev: 1, Category: "worktrees", Bucket: core.Optional, Kind: core.KindPath,
			Location: "linked-worktrees", MinAge: 30 * 24 * time.Hour},
		{Pack: "worktrees", ID: "prune", Rev: 1, Category: "worktree-prune", Bucket: core.Auto, Kind: core.KindCache,
			Tool: "git", Action: "worktree-prune", MinAge: time.Hour},
	}
}

type fixture struct {
	home  string
	repo  string
	env   *core.Env
	facts *testutil.MemFacts
}

// newFixture makes a repository that is the only root. The clock is 60 days ahead, so every worktree made
// during the test is old enough without touching any timestamp.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("SAPULIDI_TEST", "1")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	home := testutil.TempHome(t)
	repo := testutil.MakeRepo(t, filepath.Join(home, "code", "repo"))
	facts := &testutil.MemFacts{}
	env := &core.Env{
		Exclusions: testutil.NoExclusions,
		Home:       home, Now: time.Now().Add(60 * 24 * time.Hour), Roots: []core.Root{{Path: repo, Source: "config"}},
		Rules: testRules(), Platform: &testutil.FakePlatform{Temp: filepath.Join(home, "tmp"), Free: 100 << 30},
		Run: &testutil.FakeRunner{}, Settings: &testutil.FakeSettings{ConsentAll: true, Categories: map[string]bool{"worktrees": true}},
		Log: &testutil.MemLogger{}, Facts: facts, Ctx: context.Background(), TempDir: filepath.Join(home, "tmp"),
	}
	return &fixture{home: home, repo: repo, env: env, facts: facts}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// addWorktree creates a linked worktree on a new branch at path.
func (f *fixture) addWorktree(t *testing.T, path, branch string) {
	t.Helper()
	git(t, f.repo, "worktree", "add", "-b", branch, path)
}

func (f *fixture) wtPath(name string) string { return filepath.Join(f.repo, ".worktrees", name) }

func (f *fixture) planWorktrees() engine.Summary {
	return engine.Plan(f.env, []core.Category{&worktreesCat{}}, engine.Options{})
}

// scan calls Scan directly: during an engine run the engine's own fact sink replaces env.Facts.
func (f *fixture) scan(t *testing.T) {
	t.Helper()
	if _, _, err := (&worktreesCat{}).Scan(f.env); err != nil {
		t.Fatal(err)
	}
}

func removable(s engine.Summary, path string) bool {
	for _, c := range s.WouldRemove {
		if c.Path == path {
			return true
		}
	}
	return false
}

func keptWhy(s engine.Summary, path string) string {
	for _, k := range s.Kept {
		if k.Path == path {
			return k.Why
		}
	}
	return ""
}

func TestWorktreeOldCleanPushedIsCandidate(t *testing.T) {
	f := newFixture(t)
	p := f.wtPath("old")
	f.addWorktree(t, p, "old")
	s := f.planWorktrees()
	if !removable(s, p) {
		t.Fatalf("old clean pushed worktree not a candidate: %+v", s)
	}
	for _, c := range s.WouldRemove {
		if c.Path == p && (c.Removal != core.WorktreeRemove || c.Dir != f.repo || c.Base != f.repo) {
			t.Fatalf("wrong candidate: %+v", c)
		}
	}
	if len(f.facts.Facts) != 0 {
		t.Fatalf("nothing is kept, no fact expected: %v", f.facts.Facts)
	}
}

func TestWorktreeUnpushedKept(t *testing.T) {
	f := newFixture(t)
	p := f.wtPath("work")
	f.addWorktree(t, p, "work")
	if err := os.WriteFile(filepath.Join(p, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, p, "add", "new.txt")
	git(t, p, "commit", "-m", "local only")
	s := f.planWorktrees()
	if removable(s, p) {
		t.Fatal("worktree with an unpushed commit must not be a candidate")
	}
	if why := keptWhy(s, p); why != "unpushed" {
		t.Fatalf("kept why = %q, want unpushed", why)
	}
	f.scan(t)
	if got := f.facts.Facts["worktrees-kept"]; got != "1 old worktree kept because of unsaved work" {
		t.Fatalf("fact = %q", got)
	}
}

func TestWorktreeOutsideRootsIgnored(t *testing.T) {
	f := newFixture(t)
	out := filepath.Join(f.home, "elsewhere", "wt")
	f.addWorktree(t, out, "outside")
	s := f.planWorktrees()
	if removable(s, out) || keptWhy(s, out) != "" {
		t.Fatalf("a worktree outside every root must be ignored: %+v", s)
	}
}

func TestOrphanReportedNeverRemoved(t *testing.T) {
	f := newFixture(t)
	orphan := f.wtPath("gone")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, ".git"), []byte("gitdir: "+filepath.Join(f.home, "no", "such", "admin")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "notes.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := f.planWorktrees()
	if removable(s, orphan) {
		t.Fatal("an orphan must never be a candidate")
	}
	if why := keptWhy(s, orphan); why != "orphan" {
		t.Fatalf("kept why = %q, want orphan", why)
	}
	f.scan(t)
	if got := f.facts.Facts["worktrees-kept"]; !strings.Contains(got, "kept because of unsaved work") {
		t.Fatalf("fact = %q", got)
	}
	if _, err := os.Stat(filepath.Join(orphan, "notes.txt")); err != nil {
		t.Fatal("orphan files must be untouched")
	}
}

func TestPruneCandidateOnlyWhenPrunable(t *testing.T) {
	f := newFixture(t)
	p := f.wtPath("tmp")
	f.addWorktree(t, p, "tmp")
	cat := &pruneCat{}
	cands, _, err := cat.Scan(f.env)
	if err != nil || len(cands) != 0 {
		t.Fatalf("live worktree must not give a prune candidate: %v %v", cands, err)
	}
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	cands, _, err = cat.Scan(f.env)
	if err != nil || len(cands) != 1 {
		t.Fatalf("want one prune candidate, got %v %v", cands, err)
	}
	c := cands[0]
	want := guard.GitArgv(f.repo, "worktree", "prune")
	if c.Removal != core.ToolCommand || strings.Join(c.Command, " ") != strings.Join(want, " ") || c.Dir != f.repo {
		t.Fatalf("wrong candidate: %+v", c)
	}
	if err := cat.Proof(f.env, c); err != nil {
		t.Fatalf("proof: %v", err)
	}
	s := engine.Plan(f.env, []core.Category{cat}, engine.Options{})
	if len(s.WouldRemove) != 1 {
		t.Fatalf("engine kept the prune candidate: %+v", s)
	}
	git(t, f.repo, "worktree", "prune")
	if err := cat.Proof(f.env, c); err == nil {
		t.Fatal("proof must fail once nothing is prunable")
	}
}

func TestKeptNotifiedOnceUntilChanged(t *testing.T) {
	f := newFixture(t)
	dirty := func(name string) {
		p := f.wtPath(name)
		f.addWorktree(t, p, name)
		if err := os.WriteFile(filepath.Join(p, "README"), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dirty("a")
	f.scan(t)
	first := f.facts.Facts["worktrees-kept"]
	f.scan(t)
	if again := f.facts.Facts["worktrees-kept"]; again != first || first == "" {
		t.Fatalf("same state must give the same fact text: %q vs %q", first, again)
	}
	dirty("b")
	f.scan(t)
	if changed := f.facts.Facts["worktrees-kept"]; changed == first || changed != "2 old worktrees kept because of unsaved work" {
		t.Fatalf("fact did not change with the set: %q", changed)
	}
}

func (f *fixture) prune(t *testing.T) ([]core.Candidate, []core.Kept) {
	t.Helper()
	c, k, err := (&pruneCat{}).Scan(f.env)
	if err != nil {
		t.Fatal(err)
	}
	return c, k
}

// (a) a worktree on a volume that was moved away: its parent folder is missing too.
func TestPruneNoCandidateWhenVolumeMissing(t *testing.T) {
	f := newFixture(t)
	gone := f.wtPath("gone")
	f.addWorktree(t, gone, "gone")
	vol := filepath.Join(f.home, "code", "vol")
	f.addWorktree(t, filepath.Join(vol, "w3"), "w3")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(vol, vol+".gone"); err != nil {
		t.Fatal(err)
	}
	cands, kept := f.prune(t)
	if len(cands) != 0 {
		t.Fatalf("no prune candidate while an entry's volume is missing: %+v", cands)
	}
	if len(kept) != 1 || kept[0].Why != "orphan" || !strings.HasSuffix(kept[0].Path, "/code/vol/w3") {
		t.Fatalf("the unsafe entry must be reported: %+v", kept)
	}
}

// (b) the folder exists with a file but its .git file is gone: git calls it prunable, we must not.
func TestPruneNoCandidateWhenFolderStillExists(t *testing.T) {
	f := newFixture(t)
	w1 := f.wtPath("w1")
	f.addWorktree(t, w1, "w1")
	if err := os.WriteFile(filepath.Join(w1, "notes.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(w1, ".git")); err != nil {
		t.Fatal(err)
	}
	cands, kept := f.prune(t)
	if len(cands) != 0 {
		t.Fatalf("a folder that still exists must not be pruned around: %+v", cands)
	}
	if len(kept) != 1 || kept[0].Why != "orphan" || kept[0].Path != w1 {
		t.Fatalf("the folder must be reported: %+v", kept)
	}
	f.scan(t) // the worktrees category is on, so its scan owns the fact
	if got := f.facts.Facts["worktrees-kept"]; got != "1 old worktree kept because of unsaved work" {
		t.Fatalf("fact = %q", got)
	}
	c := core.Candidate{Category: "worktree-prune", RuleKey: "worktrees/prune@1", Removal: core.ToolCommand,
		Command: pruneCommand(f.repo), Dir: f.repo}
	if err := (&pruneCat{}).Proof(f.env, c); err == nil {
		t.Fatal("proof must refuse while a prunable folder still exists")
	}
}

// (c) the directory is deleted and its parent exists, for two entries.
func TestPruneCandidateWhenAllGone(t *testing.T) {
	f := newFixture(t)
	for _, n := range []string{"x", "y"} {
		f.addWorktree(t, f.wtPath(n), n)
		if err := os.RemoveAll(f.wtPath(n)); err != nil {
			t.Fatal(err)
		}
	}
	cands, kept := f.prune(t)
	if len(cands) != 1 || len(kept) != 0 {
		t.Fatalf("want one candidate and nothing kept: %+v %+v", cands, kept)
	}
	if strings.Contains(cands[0].Reason, "folders that are gone") && !strings.Contains(cands[0].Reason, "2") {
		t.Fatalf("reason: %q", cands[0].Reason)
	}
}

func TestNewlineInPathNotTruncated(t *testing.T) {
	f := newFixture(t)
	p := f.wtPath("a\nb")
	f.addWorktree(t, p, "nl")
	s := f.planWorktrees()
	if !removable(s, p) {
		t.Fatalf("worktree with a newline in its path not found: %+v", s)
	}
}

func TestLockedKeptAndCounted(t *testing.T) {
	f := newFixture(t)
	p := f.wtPath("lk")
	f.addWorktree(t, p, "lk")
	git(t, f.repo, "worktree", "lock", p)
	s := f.planWorktrees()
	if removable(s, p) || keptWhy(s, p) != "locked" {
		t.Fatalf("locked worktree must be kept as locked: %+v", s)
	}
	f.scan(t)
	if got := f.facts.Facts["worktrees-kept"]; got != "1 old worktree kept because of unsaved work" {
		t.Fatalf("fact = %q", got)
	}
}

// With the worktrees category off, prune reports the entries it refuses through the same fact.
func TestPruneReportsFactWhenWorktreesCategoryOff(t *testing.T) {
	f := newFixture(t)
	f.env.Settings = &testutil.FakeSettings{ConsentAll: true}
	w1 := f.wtPath("w1")
	f.addWorktree(t, w1, "w1")
	if err := os.Remove(filepath.Join(w1, ".git")); err != nil {
		t.Fatal(err)
	}
	f.prune(t)
	if got := f.facts.Facts["worktrees-kept"]; got != "1 old worktree kept because of unsaved work" {
		t.Fatalf("fact = %q", got)
	}
}
