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

// useExclusions installs a list holding paths for the test and removes it again afterwards.
func useExclusions(t *testing.T, paths ...string) *exclude.List {
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
	SetExclusions(l)
	t.Cleanup(func() { SetExclusions(nil) })
	return l
}

func TestExcludedPathIsKeptByRunAndPlan(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	other := f.mk("b")
	useExclusions(t, p)
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
	useExclusions(t, inner)
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(parent)))
	if !exists(parent) || keptWhy(s.Kept, parent) != "excluded" {
		t.Fatalf("contains: %+v", s)
	}
	// An exclusion above the candidate protects it (the candidate sits inside an exclusion).
	child := filepath.Join(f.mk("holder"), "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	useExclusions(t, filepath.Dir(child))
	s = f.do(Options{}, f.cat("temp", core.Auto, f.cand(child)))
	if !exists(child) || keptWhy(s.Kept, child) != "excluded" {
		t.Fatalf("contained: %+v", s)
	}
}

// The exclusion is checked again right before the removal, for every removal kind: here it appears while the
// category's Proof runs, after the first guards passed.
func TestExclusionAddedMidRunIsHonoured(t *testing.T) {
	core.RegisterToolAction("faketool", "prune")
	for _, kind := range []string{"rename", "worktree", "tool"} {
		t.Run(kind, func(t *testing.T) {
			f := newFix(t)
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
				useExclusions(t, p)
				return nil
			}
			s := f.do(Options{}, cat)
			if len(s.Removed) != 0 || keptWhy(s.Kept, p) != "excluded" || !exists(p) {
				t.Fatalf("%+v", s)
			}
			if len(f.run.Calls) != 0 && kind != "worktree" {
				t.Fatalf("a tool ran: %v", f.run.Calls)
			}
			for _, call := range f.run.Calls {
				for _, a := range call.Argv {
					if a == "remove" {
						t.Fatalf("worktree remove ran: %v", call.Argv)
					}
				}
			}
		})
	}
}
