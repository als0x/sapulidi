package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

// offCat is an optional category that is switched off (it is not in the settings' category map).
func offCat(f *fix, cands ...core.Candidate) *fakeCat {
	return &fakeCat{name: "deps", bucket: core.Optional, cands: cands}
}

func depsCand(f *fix, p string) core.Candidate {
	return core.Candidate{Category: "deps", RuleKey: "x/deps@1", Path: p, Base: f.tmp, Removal: core.RenameDelete, Bytes: -1}
}

func logField(f *fix, kind, key string) any {
	for _, e := range f.log.Events {
		if e.Kind == kind {
			return e.Fields[key]
		}
	}
	return nil
}

func TestExplicitRemovesOffCategoryWithoutConsent(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	other := f.mk("b")
	s := f.do(Options{Explicit: []string{p}}, offCat(f, depsCand(f, p), depsCand(f, other)))
	if exists(p) || !hasPath(s.Removed, p) {
		t.Fatalf("explicit path not removed: %+v", s)
	}
	if !exists(other) || hasPath(s.Removed, other) || hasPath(s.WouldRemove, other) {
		t.Fatalf("a path outside Explicit was touched: %+v", s)
	}
	if logField(f, "summary", "trigger") != "tui" || logField(f, "summary", "explicit") != 1 {
		t.Fatalf("log %+v", f.log.Events)
	}
}

func TestExplicitWaivesAutoConsent(t *testing.T) {
	f := newFix(t)
	f.set.ConsentAll = false
	f.set.ConsentedKeys = nil
	p := f.mk("a")
	s := f.do(Options{Explicit: []string{p}}, f.cat("temp", core.Auto, f.cand(p)))
	if exists(p) || len(s.Removed) != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestWithoutExplicitOffCategoryIsNotScanned(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	s := f.do(Options{}, offCat(f, depsCand(f, p)))
	if !exists(p) || len(s.Removed)+len(s.WouldRemove) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestExplicitNotACandidate(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	ghost := f.mk("ghost")
	s := f.do(Options{Explicit: []string{p, ghost}}, f.cat("temp", core.Auto, f.cand(p)))
	if exists(p) || !exists(ghost) || keptWhy(s.Kept, ghost) != "not-a-candidate" {
		t.Fatalf("%+v", s)
	}
}

func TestExplicitKeepsWhatGuardsKeep(t *testing.T) {
	f := newFix(t)
	excl := f.mk("excluded")
	young := f.mk("young")
	testutil.Touch(t, filepath.Join(young, "data.bin"), time.Minute)
	testutil.Touch(t, young, time.Minute)
	busy := f.mk("busy")
	f.plat.Open = map[string]bool{busy: true}
	never := f.mk("never")
	useExclusions(t, excl)

	cat := offCat(f, depsCand(f, excl), depsCand(f, young), depsCand(f, busy))
	nev := &fakeCat{name: "models", bucket: core.Never, cands: []core.Candidate{
		{Category: "models", RuleKey: "x/a@1", Path: never, Base: f.tmp, Removal: core.RenameDelete}}}
	paths := []string{excl, young, busy, never}
	s := f.do(Options{Explicit: paths}, cat, nev)
	for _, p := range paths {
		if !exists(p) || hasPath(s.Removed, p) {
			t.Fatalf("%s removed: %+v", p, s)
		}
	}
	if keptWhy(s.Kept, excl) != "excluded" {
		t.Fatalf("excluded must win over Explicit: %+v", s.Kept)
	}
	if keptWhy(s.Kept, young) == "" || keptWhy(s.Kept, busy) == "" {
		t.Fatalf("young and busy must be kept: %+v", s.Kept)
	}
}

func TestExplicitRefusedWhenPausedOrBusy(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	f.st.Pause()
	s := f.do(Options{Explicit: []string{p}}, offCat(f, depsCand(f, p)))
	if s.Mode != "paused" || !exists(p) {
		t.Fatalf("%+v", s)
	}
	f2 := newFix(t)
	p2 := f2.mk("a")
	rel, err := f2.st.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	s = f2.do(Options{Explicit: []string{p2}}, offCat(f2, depsCand(f2, p2)))
	if s.Mode != "busy" || !exists(p2) {
		t.Fatalf("%+v", s)
	}
}

func TestExplicitNeverRunsLowDisk(t *testing.T) {
	f := newFix(t)
	f.set.LowDisk = 1 << 50 // free space is always below the limit
	called := false
	SetLowDisk(func(*core.Env, *Summary) []core.Candidate { called = true; return nil })
	p := f.mk("a")
	f.do(Options{Explicit: []string{p}}, f.cat("temp", core.Auto, f.cand(p)))
	if called {
		t.Fatal("low-disk tier ran with Explicit")
	}
}

func TestShowOptionalOffTagsAndRunIgnoresIt(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	cat := offCat(f, depsCand(f, p))
	s := Plan(f.env, []core.Category{cat}, Options{})
	if len(s.WouldRemove) != 0 {
		t.Fatalf("off category listed without the flag: %+v", s)
	}
	s = Plan(f.env, []core.Category{cat}, Options{ShowOptionalOff: true})
	if len(s.WouldRemove) != 1 || s.WouldRemove[0].Source != "optional-off" {
		t.Fatalf("%+v", s)
	}
	s = f.do(Options{ShowOptionalOff: true}, cat)
	if !exists(p) || len(s.Removed)+len(s.WouldRemove) != 0 {
		t.Fatalf("Run must ignore ShowOptionalOff: %+v", s)
	}
}
