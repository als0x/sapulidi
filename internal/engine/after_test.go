package engine

import (
	"errors"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/state"
)

// afterCat is a fakeCat that records AfterRemove calls.
type afterCat struct {
	*fakeCat
	after []core.Candidate
}

func (a *afterCat) AfterRemove(_ *core.Env, c core.Candidate) { a.after = append(a.after, c) }

func toolCand() core.Candidate {
	return core.Candidate{Category: "temp", RuleKey: "x/a@1", Removal: core.ToolCommand,
		Command: []string{"faketool", "prune"}, Bytes: -1}
}

func newAfterFix(t *testing.T, c core.Candidate) (*fix, *afterCat) {
	t.Helper()
	core.RegisterToolAction("faketool", "prune")
	f := newFix(t)
	f.run.Paths["faketool"] = "/bin/faketool"
	f.run.On([]string{"faketool", "prune"}, "", nil)
	return f, &afterCat{fakeCat: f.cat("temp", core.Auto, c)}
}

func TestAfterRemoveCalledOnSuccess(t *testing.T) {
	f, ac := newAfterFix(t, toolCand())
	s := f.do(Options{}, ac)
	if len(s.Removed) != 1 || len(ac.after) != 1 {
		t.Fatalf("removed %d, after %d", len(s.Removed), len(ac.after))
	}
}

func TestAfterRemoveCalledForRenameDelete(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	ac := &afterCat{fakeCat: f.cat("temp", core.Auto, f.cand(p))}
	f.do(Options{}, ac)
	if len(ac.after) != 1 || ac.after[0].Path != p {
		t.Fatalf("after = %+v", ac.after)
	}
}

func TestAfterRemoveNotCalledOnFailure(t *testing.T) {
	f, ac := newAfterFix(t, toolCand())
	f.run.On([]string{"faketool", "prune"}, "", errors.New("exit status 1"))
	s := f.do(Options{}, ac)
	if len(s.Failed) != 1 || len(ac.after) != 0 {
		t.Fatalf("failed %d, after %d", len(s.Failed), len(ac.after))
	}
}

func TestAfterRemoveNotCalledWhenKept(t *testing.T) {
	f, ac := newAfterFix(t, toolCand())
	ac.proof = core.Keep("error", "no")
	f.do(Options{}, ac)
	if len(ac.after) != 0 {
		t.Fatal("a kept candidate was not removed")
	}
}

func TestAfterRemoveNotCalledInDryRunPlanOrForcedDry(t *testing.T) {
	f, ac := newAfterFix(t, toolCand())
	f.do(Options{DryRun: true}, ac)
	Plan(f.env, []core.Category{ac}, Options{})
	SetBeforeRun(func(*core.Env, *state.Store, Options) string { return "error" })
	f.do(Options{}, ac)
	if len(ac.after) != 0 {
		t.Fatalf("a dry run must not call AfterRemove: %+v", ac.after)
	}
}

func TestQuickRunSkipsLowDiskHook(t *testing.T) {
	f, a, _ := lowDiskFix(t)
	called := false
	SetLowDisk(func(*core.Env, *Summary) []core.Candidate { called = true; return nil })
	f.do(Options{Quick: true}, f.cat("temp", core.Auto))
	if called || !exists(a) {
		t.Fatal("a quick run must not call the low-disk hook")
	}
}

func TestAfterRemovePanicIgnored(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	pc := &panicAfter{afterCat{fakeCat: f.cat("temp", core.Auto, f.cand(p))}}
	s := f.do(Options{}, pc)
	if len(s.Removed) != 1 || len(s.Failed) != 0 || exists(p) {
		t.Fatalf("%+v", s)
	}
}

type panicAfter struct{ afterCat }

func (*panicAfter) AfterRemove(*core.Env, core.Candidate) { panic("after exploded") }
