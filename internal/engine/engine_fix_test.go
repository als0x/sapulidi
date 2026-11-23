package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

func TestLowDiskSkipsDisabledOptionalCategory(t *testing.T) {
	f, a, b := lowDiskFix(t)
	ca, cb := f.cand(a), f.cand(b)
	ca.Category, cb.Category = "deps", "deps"
	f.cat("deps", core.Optional)
	f.set.Categories["deps"] = false
	SetLowDisk(func(*core.Env, *Summary) []core.Candidate { return []core.Candidate{ca, cb} })
	s := f.do(Options{}, f.cat("temp", core.Auto), &fakeCat{name: "deps", bucket: core.Optional})
	if !exists(a) || !exists(b) || len(s.Removed) != 0 || len(s.Kept) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestLowDiskSkipsNeverCategory(t *testing.T) {
	f, a, _ := lowDiskFix(t)
	ca := f.cand(a)
	ca.Category = "models"
	f.set.Categories["models"] = true
	SetLowDisk(func(*core.Env, *Summary) []core.Candidate { return []core.Candidate{ca} })
	s := f.do(Options{}, f.cat("temp", core.Auto), &fakeCat{name: "models", bucket: core.Never})
	if !exists(a) || len(s.Removed) != 0 || len(s.Kept) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestLowDiskNotRunWithPathsOrOnly(t *testing.T) {
	for _, opt := range []func(a string) Options{
		func(a string) Options { return Options{Paths: []string{a}} },
		func(string) Options { return Options{Only: []string{"temp"}} },
	} {
		f, a, b := lowDiskFix(t)
		called := false
		SetLowDisk(func(*core.Env, *Summary) []core.Candidate { called = true; return []core.Candidate{f.cand(b)} })
		f.do(opt(a), f.cat("temp", core.Auto))
		if called || !exists(b) {
			t.Fatal("low-disk tier ran in a clean-once or filtered run")
		}
	}
}

func TestUnknownRuleKeyKeptForEveryKind(t *testing.T) {
	core.RegisterToolAction("faketool", "prune")
	for _, key := range []string{"", "x/gone@1", "x/a@0"} {
		f := newFix(t)
		f.run.Paths["faketool"] = "/bin/faketool"
		f.run.On([]string{"faketool"}, "", nil)
		f.run.On([]string{"git"}, "", nil)
		a, w, tp := f.mk("a"), f.mk("wt"), f.mk("tp")
		ca := f.cand(a)
		cw := worktreeCand(f, w)
		ct := core.Candidate{Category: "temp", Path: tp, Base: f.tmp, Removal: core.ToolCommand, Command: []string{"faketool", "prune"}}
		cn := core.Candidate{Category: "temp", Removal: core.ToolCommand, Command: []string{"faketool", "prune"}}
		for _, c := range []*core.Candidate{&ca, &cw, &ct, &cn} {
			c.RuleKey = key
		}
		cw.Category = "temp"
		s := f.do(Options{}, f.cat("temp", core.Auto, ca, cw, ct, cn))
		if len(s.Removed) != 0 || len(s.Kept) != 4 || len(f.run.Calls) != 0 || !exists(a) || !exists(w) || !exists(tp) {
			t.Fatalf("key %q: %+v calls=%v", key, s, f.run.Calls)
		}
		for _, k := range s.Kept {
			if k.Why != "error" {
				t.Fatalf("key %q: why %q", key, k.Why)
			}
		}
	}
}

func TestScanPanicRecovered(t *testing.T) {
	f := newFix(t)
	boom := f.cat("temp", core.Auto)
	boom.scan = func(*core.Env) { panic("scan exploded") }
	p := f.mk("ok")
	c := f.cand(p)
	c.Category, c.RuleKey = "deps", "x/deps@1"
	good := f.cat("deps", core.Optional, c)
	s := f.do(Options{}, boom, good)
	if exists(p) || len(s.Failed) != 1 || s.Failed[0].Why != "error" || s.Failed[0].Category != "temp" {
		t.Fatalf("%+v", s)
	}
	if _, ok := f.st.LastRun(); !ok {
		t.Fatal("run not recorded")
	}
	rel, err := f.st.Lock()
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	rel()
	found := false
	for _, e := range f.log.Events {
		found = found || e.Kind == "scan-failed"
	}
	if !found {
		t.Fatal("no log event")
	}
}

func TestProofPanicKept(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	cat := f.cat("temp", core.Auto, f.cand(p))
	cat.proofFn = func(core.Candidate) error { panic("proof exploded") }
	s := f.do(Options{}, cat)
	if !exists(p) || keptWhy(s.Kept, p) != "error" {
		t.Fatalf("%+v", s)
	}
	if _, ok := f.st.LastRun(); !ok {
		t.Fatal("run not recorded")
	}
}

func TestParentSwapDuringProofKept(t *testing.T) {
	f := newFix(t)
	par := filepath.Join(f.tmp, "par")
	child := filepath.Join(par, "child")
	victim := filepath.Join(f.tmp, "victim")
	for _, d := range []string{child, filepath.Join(victim, "child")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(d, "data.bin")
		if err := os.WriteFile(file, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-48 * time.Hour)
		_ = os.Chtimes(file, old, old)
		_ = os.Chtimes(d, old, old)
	}
	cat := f.cat("temp", core.Auto, f.cand(child))
	cat.proofFn = func(core.Candidate) error {
		_ = os.RemoveAll(par)
		return os.Symlink(victim, par)
	}
	s := f.do(Options{}, cat)
	if !exists(filepath.Join(victim, "child", "data.bin")) || len(s.Removed) != 0 || keptWhy(s.Kept, child) != "place" {
		t.Fatalf("%+v", s)
	}
}

func TestBaseOutsideAllowedKept(t *testing.T) {
	f := newFix(t)
	docs := filepath.Join(f.home, "Documents")
	for _, base := range []string{"/", docs, f.home} {
		p := filepath.Join(docs, "x")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(p, "data.bin")
		_ = os.WriteFile(file, make([]byte, 10), 0o644)
		old := time.Now().Add(-48 * time.Hour)
		_ = os.Chtimes(file, old, old)
		_ = os.Chtimes(p, old, old)
		c := f.cand(p)
		c.Base = base
		s := f.do(Options{}, f.cat("temp", core.Auto, c))
		if !exists(file) || keptWhy(s.Kept, p) != "place" {
			t.Fatalf("base %q: %+v", base, s)
		}
	}
}

func TestBaseUnderRootAllowed(t *testing.T) {
	f := newFix(t)
	root := filepath.Join(f.home, "Code")
	p := filepath.Join(root, "proj", "build")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(p, "o.bin")
	_ = os.WriteFile(file, make([]byte, 10), 0o644)
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(file, old, old)
	_ = os.Chtimes(p, old, old)
	f.env.Roots = []core.Root{{Path: root, Source: "config"}}
	c := f.cand(p)
	c.Base = root
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if exists(p) || len(s.Removed) != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestWaitingOnlyAfterGuardsPass(t *testing.T) {
	f := newFix(t)
	f.set.ConsentAll = false
	f.set.ConsentedKeys = map[string]bool{}
	p := f.mk("a")
	f.plat.Open = map[string]bool{p: true}
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if len(s.Waiting) != 0 || keptWhy(s.Kept, p) != "open" {
		t.Fatalf("%+v", s)
	}
}

func TestDryRunDoesNotNotifyOrConsumeFlag(t *testing.T) {
	f := newFix(t)
	f.set.Notify = true
	cat := f.cat("temp", core.Auto)
	cat.facts = map[string]string{"k": "text"}
	f.do(Options{DryRun: true}, cat)
	if len(f.plat.Notifications) != 0 {
		t.Fatalf("dry run notified: %v", f.plat.Notifications)
	}
	f.do(Options{}, cat)
	if len(f.plat.Notifications) != 1 {
		t.Fatalf("real run did not notify: %v", f.plat.Notifications)
	}
}

func TestFinishHonoursContext(t *testing.T) {
	f := newFix(t)
	left := filepath.Join(f.tmp, ".sapulidi-removing-x")
	if err := os.MkdirAll(left, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.env.Ctx = ctx
	Finish(f.env, []string{f.tmp})
	if !exists(left) {
		t.Fatal("Finish removed after the context was done")
	}
}
