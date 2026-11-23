package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/exclude"
)

// useExclusions installs a list holding paths on the fixture's env and returns it (a test may add to it later).
func useExclusions(t *testing.T, f *fix, paths ...string) *exclude.List {
	t.Helper()
	l, err := exclude.Load(config.Paths{Config: filepath.Join(t.TempDir(), "config.toml")})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if _, _, err := l.Add(p, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	f.env.Exclusions = l
	return l
}

func TestExcludedPathIsKeptByRunAndPlan(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	other := f.mk("b")
	useExclusions(t, f, p)
	cat := f.cat("temp", core.Auto, f.cand(p), f.cand(other))
	s := f.do(Options{}, cat)
	if !exists(p) || keptWhy(s.Kept, p) != "excluded" || hasPath(s.Removed, p) {
		t.Fatalf("run: %+v", s)
	}
	if exists(other) {
		t.Fatalf("an unrelated path must still be removed: %+v", s)
	}
	pl := Plan(f.env, []core.Category{f.cat("temp", core.Auto, f.cand(p))}, Options{})
	if keptWhy(pl.Kept, p) != "excluded" || hasPath(pl.WouldRemove, p) {
		t.Fatalf("plan: %+v", pl)
	}
}

func TestExcludedFolderAboveAndBelow(t *testing.T) {
	f := newFix(t)
	parent := f.mk("parent")
	inner := filepath.Join(parent, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	// An exclusion below the candidate protects it (the candidate contains an exclusion).
	useExclusions(t, f, inner)
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(parent)))
	if !exists(parent) || keptWhy(s.Kept, parent) != "excluded" {
		t.Fatalf("contains: %+v", s)
	}
	// An exclusion above the candidate protects it (the candidate sits inside an exclusion).
	child := filepath.Join(f.mk("holder"), "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	useExclusions(t, f, filepath.Dir(child))
	s = f.do(Options{}, f.cat("temp", core.Auto, f.cand(child)))
	if !exists(child) || keptWhy(s.Kept, child) != "excluded" {
		t.Fatalf("contained: %+v", s)
	}
}

func TestExcludedBaseKeepsCandidate(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	useExclusions(t, f, f.tmp) // the candidate's Base is excluded
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if !exists(p) || keptWhy(s.Kept, p) != "excluded" {
		t.Fatalf("%+v", s)
	}
}

// The exclusion is checked again right before the removal, for every removal kind: here it appears while the
// category's Proof runs, after the first guards passed.
func TestExclusionAddedMidRunIsHonoured(t *testing.T) {
	core.RegisterToolAction("faketool", "prune")
	for _, kind := range []string{"rename", "worktree", "tool"} {
		t.Run(kind, func(t *testing.T) {
			f := newFix(t)
			l := useExclusions(t, f)
			f.run.Paths["faketool"] = "/bin/faketool"
			f.run.On([]string{"faketool", "prune"}, "", nil)
			f.run.On([]string{"git"}, "", nil)
			p := f.mk("a")
			c := f.cand(p)
			cat := f.cat("temp", core.Auto, c)
			switch kind {
			case "worktree":
				c = worktreeCand(f, p)
				cat = f.cat("worktrees", core.Optional, c)
			case "tool":
				c.Removal, c.Command = core.ToolCommand, []string{"faketool", "prune"}
				cat = f.cat("temp", core.Auto, c)
			}
			cat.proofFn = func(core.Candidate) error {
				if _, _, err := l.Add(p, "", time.Now()); err != nil {
					t.Error(err)
				}
				return nil
			}
			s := f.do(Options{}, cat)
			if len(s.Removed) != 0 || keptWhy(s.Kept, p) != "excluded" || !exists(p) {
				t.Fatalf("%+v", s)
			}
			for _, call := range f.run.Calls {
				for _, a := range call.Argv {
					if a == "remove" || a == "prune" {
						t.Fatalf("a removal command ran: %v", call.Argv)
					}
				}
			}
		})
	}
}

// The re-check right before the rename is the last one: an exclusion that appears during the second Proof call (the
// one inside renameDelete) still wins.
func TestExclusionAppearingInSecondProofWins(t *testing.T) {
	f := newFix(t)
	l := useExclusions(t, f)
	p := f.mk("a")
	cat := f.cat("temp", core.Auto, f.cand(p))
	calls := 0
	cat.proofFn = func(core.Candidate) error {
		calls++
		if calls == 2 {
			if _, _, err := l.Add(p, "", time.Now()); err != nil {
				t.Error(err)
			}
		}
		return nil
	}
	s := f.do(Options{}, cat)
	if calls != 2 || len(s.Removed) != 0 || !exists(p) || keptWhy(s.Kept, p) != "excluded" {
		t.Fatalf("calls=%d %+v", calls, s)
	}
}

func TestRunAndPlanRefuseWithoutExclusionsLoaded(t *testing.T) {
	f := newFix(t)
	f.env.Exclusions = nil
	p := f.mk("a")
	cat := f.cat("temp", core.Auto, f.cand(p))
	s := f.do(Options{}, cat)
	if s.Mode != "error" || cat.scanned != 0 || !exists(p) {
		t.Fatalf("run: %+v", s)
	}
	pl := Plan(f.env, []core.Category{cat}, Options{})
	if pl.Mode != "error" || cat.scanned != 0 {
		t.Fatalf("plan: %+v", pl)
	}
}

func TestExcludedDirOfPruneAndWorktreeRemove(t *testing.T) {
	f := newFix(t)
	f.run.Paths["git"] = "/usr/bin/git"
	f.run.On([]string{"git"}, "", nil)
	repo := filepath.Join(f.home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	useExclusions(t, f, repo)

	prune := core.Candidate{Category: "worktrees", RuleKey: "x/wt@1", Removal: core.ToolCommand, Dir: repo,
		Command: []string{"git", "-C", repo, "worktree", "prune"}, Bytes: -1}
	wtPath := f.mk("wt")
	wt := worktreeCand(f, wtPath) // its Dir is f.home/repo, the excluded main repository
	s := f.do(Options{}, f.cat("worktrees", core.Optional, prune, wt))
	if len(s.Removed) != 0 || !exists(wtPath) || len(f.run.Calls) != 0 {
		t.Fatalf("%+v calls=%v", s, f.run.Calls)
	}
	if keptWhy(s.Kept, wtPath) != "excluded" {
		t.Fatalf("worktree: %+v", s.Kept)
	}
	found := false
	for _, k := range s.Kept {
		if k.Path == "" && k.Why == "excluded" {
			found = true
		}
	}
	if !found {
		t.Fatalf("prune not kept as excluded: %+v", s.Kept)
	}
}
