package engine

import (
	"context"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/testutil"
)

// panicRunner is a runner whose Run panics.
type panicRunner struct{ *testutil.FakeRunner }

func (panicRunner) Run(context.Context, string, ...string) ([]byte, error) { panic("runner exploded") }

func TestRemovalPanicIsFailedAndRunContinues(t *testing.T) {
	core.RegisterToolAction("faketool", "prune")
	f := newFix(t)
	f.run.Paths["faketool"] = "/bin/faketool"
	f.env.Run = panicRunner{f.run}
	ct := core.Candidate{Category: "temp", RuleKey: "x/a@1", Removal: core.ToolCommand, Command: []string{"faketool", "prune"}}
	good := f.mk("good")
	wt := f.mk("wt")
	cw := worktreeCand(f, wt)
	cw.Category, cw.RuleKey = "temp", "x/a@1"
	s := f.do(Options{}, f.cat("temp", core.Auto, ct, cw, f.cand(good)))
	if len(s.Failed) != 2 || s.Failed[0].Why != "error" || exists(good) || !hasPath(s.Removed, good) || !exists(wt) {
		t.Fatalf("%+v", s)
	}
	if _, ok := f.st.LastRun(); !ok {
		t.Fatal("run not recorded")
	}
}

func TestBeforeRunPanicForcesErrorMode(t *testing.T) {
	f := newFix(t)
	SetBeforeRun(func(*core.Env, *state.Store, Options) string { panic("before exploded") })
	p := f.mk("a")
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if s.Mode != "error" || !exists(p) || !hasPath(s.WouldRemove, p) {
		t.Fatalf("%+v", s)
	}
}

func TestLowDiskPanicIgnored(t *testing.T) {
	f, a, _ := lowDiskFix(t)
	SetLowDisk(func(*core.Env, *Summary) []core.Candidate { panic("low exploded") })
	s := f.do(Options{}, f.cat("temp", core.Auto))
	if s.Mode != "run" || !exists(a) {
		t.Fatalf("%+v", s)
	}
	if _, ok := f.st.LastRun(); !ok {
		t.Fatal("run not recorded")
	}
}

func TestAfterRunPanicIgnored(t *testing.T) {
	f := newFix(t)
	SetAfterRun(func(*core.Env, *state.Store, *Summary) { panic("after exploded") })
	p := f.mk("a")
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if exists(p) || s.Mode != "run" {
		t.Fatalf("%+v", s)
	}
	if _, ok := f.st.LastRun(); !ok {
		t.Fatal("run not recorded")
	}
}

func TestPlanSurvivesPanics(t *testing.T) {
	f := newFix(t)
	boom := f.cat("deps", core.Optional)
	boom.scan = func(*core.Env) { panic("scan exploded") }
	p := f.mk("a")
	cat := f.cat("temp", core.Auto, f.cand(p))
	cat.proofFn = func(core.Candidate) error { panic("proof exploded") }
	s := Plan(f.env, []core.Category{boom, cat}, Options{})
	if len(s.Failed) != 1 || keptWhy(s.Kept, p) != "error" || !exists(p) {
		t.Fatalf("%+v", s)
	}
}

func TestCandidateWithOtherCategorysRuleKeyKept(t *testing.T) {
	f := newFix(t)
	a, b := f.mk("a"), f.mk("b")
	c1 := f.cand(a) // category temp, rule of deps
	c1.RuleKey = "x/deps@1"
	c2 := f.cand(b) // category deps, rule of temp, scanned by temp
	c2.Category = "deps"
	s := f.do(Options{}, f.cat("temp", core.Auto, c1, c2))
	if !exists(a) || !exists(b) || keptWhy(s.Kept, a) != "error" || keptWhy(s.Kept, b) != "error" {
		t.Fatalf("%+v", s)
	}
}

func TestLowDiskCandidateWithOtherCategorysRuleKeyKept(t *testing.T) {
	f, a, _ := lowDiskFix(t)
	c := f.cand(a)
	c.RuleKey = "x/deps@1"
	SetLowDisk(func(*core.Env, *Summary) []core.Candidate { return []core.Candidate{c} })
	s := f.do(Options{}, f.cat("temp", core.Auto))
	if !exists(a) || keptWhy(s.Kept, a) != "error" {
		t.Fatalf("%+v", s)
	}
}

func TestAutoRuleInOptionalCategoryStillNeedsConsent(t *testing.T) {
	f := newFix(t)
	f.set.ConsentAll = false
	f.env.Rules = append(f.env.Rules, core.Rule{Pack: "x", ID: "dauto", Rev: 1, Category: "deps", Bucket: core.Auto, Kind: core.KindProject, MinAge: 24 * time.Hour})
	p := f.mk("a")
	c := f.cand(p)
	c.Category, c.RuleKey = "deps", "x/dauto@1"
	s := f.do(Options{}, f.cat("deps", core.Optional, c))
	if !exists(p) || len(s.Waiting) != 1 || s.Waiting[0] != "x/dauto@1" {
		t.Fatalf("%+v", s)
	}
}

func TestLowDiskCandidateNeedsConsent(t *testing.T) {
	f, a, _ := lowDiskFix(t)
	f.set.ConsentAll = false
	f.set.ConsentedKeys = map[string]bool{"x/a@1": true}
	f.consent("x/a@1")
	f.env.Rules = append(f.env.Rules, core.Rule{Pack: "x", ID: "a", Rev: 2, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp, MinAge: time.Hour})
	c := f.cand(a)
	c.RuleKey = "x/a@2"
	SetLowDisk(func(*core.Env, *Summary) []core.Candidate { return []core.Candidate{c} })
	s := f.do(Options{}, f.cat("temp", core.Auto))
	if !exists(a) || len(s.Removed) != 0 || len(s.Waiting) != 1 || s.Waiting[0] != "x/a@2" {
		t.Fatalf("%+v", s)
	}
}

func TestReportOnlyWithEmptyRuleKeyIsSilent(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	c := f.cand(p)
	c.Removal, c.RuleKey = core.ReportOnly, ""
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if !exists(p) || len(s.Kept)+len(s.Removed)+len(s.WouldRemove)+len(s.Failed) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestRenameRechecksFreshAndInUse(t *testing.T) {
	f := newFix(t)
	young, open := f.mk("young"), f.mk("open")
	cat := f.cat("temp", core.Auto, f.cand(young), f.cand(open))
	cat.proofFn = func(c core.Candidate) error {
		if c.Path == young {
			testutil.Touch(t, c.Path+"/data.bin", 0)
		} else {
			f.plat.Open = map[string]bool{open: true}
		}
		return nil
	}
	s := f.do(Options{}, cat)
	if !exists(young) || !exists(open) || keptWhy(s.Kept, young) != "young" || keptWhy(s.Kept, open) != "open" {
		t.Fatalf("%+v", s)
	}
}

func TestBudgetExpiringDuringSizingIsKept(t *testing.T) {
	f := newFix(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.env.Ctx = ctx
	p := f.mk("a")
	cat := f.cat("temp", core.Auto, f.cand(p))
	cat.proofFn = func(core.Candidate) error { cancel(); return nil } // the last guard; sizing comes next
	s := f.do(Options{}, cat)
	if !s.Stopped || !exists(p) || keptWhy(s.Kept, p) != "budget" {
		t.Fatalf("%+v", s)
	}
}
