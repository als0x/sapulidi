package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

// offCat is an optional category that is switched off (it is not in the settings' category map).
func offCat(cands ...core.Candidate) *fakeCat {
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
	s := f.do(Options{Explicit: []string{p}}, offCat(depsCand(f, p), depsCand(f, other)))
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
	s := f.do(Options{}, offCat(depsCand(f, p)))
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

// A different spelling of a candidate's path selects nothing: it is not-a-candidate, which is safe.
func TestExplicitSpellingsOfAPathAreNotCandidates(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	link := filepath.Join(f.tmp, "link-to-a")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(f.home, p)
	if err != nil {
		t.Fatal(err)
	}
	for name, spelling := range map[string]string{"symlink": link, "relative": rel, "trailing slash": p + "/",
		"dot segment": filepath.Join(f.tmp, ".", "a") + "/../a"} {
		s := f.do(Options{Explicit: []string{spelling}}, f.cat("temp", core.Auto, f.cand(p)))
		if !exists(p) || len(s.Removed) != 0 || keptWhy(s.Kept, spelling) != "not-a-candidate" {
			t.Fatalf("%s: %+v", name, s)
		}
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
	useExclusions(t, f, excl)

	cat := offCat(depsCand(f, excl), depsCand(f, young), depsCand(f, busy))
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

// A rule of the never bucket inside an enabled category is never removed, Explicit or not.
func TestExplicitNeverRuleInEnabledCategory(t *testing.T) {
	f := newFix(t)
	f.env.Rules = append(f.env.Rules, core.Rule{Pack: "x", ID: "nv", Rev: 1, Category: "temp", Bucket: core.Never,
		Kind: core.KindTemp, MinAge: time.Hour})
	p := f.mk("a")
	c := f.cand(p)
	c.RuleKey = "x/nv@1"
	s := f.do(Options{Explicit: []string{p}}, f.cat("temp", core.Auto, c))
	if !exists(p) || len(s.Removed) != 0 || keptWhy(s.Kept, p) != "not-a-candidate" {
		t.Fatalf("%+v", s)
	}
}

// The guard matrix for the two kinds that are not a rename: worktree remove and tool command.
func TestExplicitGuardMatrixWorktreeAndTool(t *testing.T) {
	core.RegisterToolAction("faketool", "prune")
	type setup func(f *fix, p string) (core.Candidate, *fakeCat)
	wt := func(f *fix, p string) (core.Candidate, *fakeCat) {
		c := worktreeCand(f, p)
		return c, &fakeCat{name: "worktrees", bucket: core.Optional, cands: []core.Candidate{c}}
	}
	tool := func(f *fix, p string) (core.Candidate, *fakeCat) {
		c := f.cand(p)
		c.Category, c.RuleKey = "deps", "x/deps@1"
		c.Removal, c.Command = core.ToolCommand, []string{"faketool", "prune"}
		return c, &fakeCat{name: "deps", bucket: core.Optional, cands: []core.Candidate{c}}
	}
	for kind, mk := range map[string]setup{"worktree": wt, "tool": tool} {
		for _, why := range []string{"excluded", "in-use", "young"} {
			if kind == "tool" && why == "in-use" {
				continue // a tool command has no in-use guard of its own
			}
			t.Run(kind+"/"+why, func(t *testing.T) {
				f := newFix(t)
				f.run.Paths["faketool"] = "/bin/faketool"
				f.run.On([]string{"faketool", "prune"}, "", nil)
				f.run.On([]string{"git"}, "", nil)
				p := f.mk("a")
				_, cat := mk(f, p)
				switch why {
				case "excluded":
					useExclusions(t, f, p)
				case "in-use":
					f.plat.Open = map[string]bool{p: true}
				case "young":
					// the category's Proof is where a worktree or a tool target is judged too young
					cat.proof = core.Keep("fresh", "young")
				}
				s := f.do(Options{Explicit: []string{p}}, cat)
				if len(s.Removed) != 0 || !exists(p) || keptWhy(s.Kept, p) == "" {
					t.Fatalf("%+v", s)
				}
				if why == "excluded" && keptWhy(s.Kept, p) != "excluded" {
					t.Fatalf("excluded must win: %+v", s.Kept)
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
}

func TestExplicitRefusedWhenPausedOrBusy(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	f.st.Pause()
	s := f.do(Options{Explicit: []string{p}}, offCat(depsCand(f, p)))
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
	s = f2.do(Options{Explicit: []string{p2}}, offCat(depsCand(f2, p2)))
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

func TestExplicitRunDoesNotNotify(t *testing.T) {
	f := newFix(t)
	f.set.LowDisk = 1 << 50
	f.set.Notify = true
	p := f.mk("a")
	f.do(Options{Explicit: []string{p}}, f.cat("temp", core.Auto, f.cand(p)))
	if len(f.plat.Notifications) != 0 {
		t.Fatalf("an Explicit run notified: %v", f.plat.Notifications)
	}
	// The notify-once flag was not used up: a normal run afterwards still notifies.
	f.do(Options{}, f.cat("temp", core.Auto))
	if len(f.plat.Notifications) != 1 {
		t.Fatalf("the normal run after it must notify once: %v", f.plat.Notifications)
	}
}

// Whatever Explicit entries are empty, relative or unclean select nothing: a path-less candidate (worktree prune)
// must never be reached through them.
func TestExplicitBadEntriesNeverSelectPathlessCandidates(t *testing.T) {
	core.RegisterToolAction("faketool", "prune")
	for _, entry := range []string{"", "relative", "/a/../b", "/a/", "/a//b"} {
		f := newFix(t)
		f.run.Paths["faketool"] = "/bin/faketool"
		f.run.On([]string{"faketool", "prune"}, "", nil)
		prune := core.Candidate{Category: "deps", RuleKey: "x/deps@1", Removal: core.ToolCommand, Dir: f.home,
			Command: []string{"faketool", "prune"}, Bytes: -1}
		s := f.do(Options{Explicit: []string{entry}}, offCat(prune))
		if len(s.Removed) != 0 || len(f.run.Calls) != 0 {
			t.Fatalf("entry %q: removed %+v calls %v", entry, s.Removed, f.run.Calls)
		}
		if keptWhy(s.Kept, entry) != "not-a-candidate" {
			t.Fatalf("entry %q: %+v", entry, s.Kept)
		}
	}
	// A valid Explicit path alongside an empty entry still does not reach the path-less candidate.
	f := newFix(t)
	f.run.Paths["faketool"] = "/bin/faketool"
	f.run.On([]string{"faketool", "prune"}, "", nil)
	p := f.mk("a")
	prune := core.Candidate{Category: "deps", RuleKey: "x/deps@1", Removal: core.ToolCommand, Dir: f.home,
		Command: []string{"faketool", "prune"}, Bytes: -1}
	f.do(Options{Explicit: []string{"", p}}, offCat(prune, depsCand(f, p)))
	if len(f.run.Calls) != 0 || exists(p) {
		t.Fatalf("calls %v, p exists %v", f.run.Calls, exists(p))
	}
}

func TestPathsAndExplicitTogetherAreRefused(t *testing.T) {
	f := newFix(t)
	f.set.ConsentAll = false
	f.set.ConsentedKeys = nil
	a, b := f.mk("a"), f.mk("b")
	cat := f.cat("temp", core.Auto, f.cand(a), f.cand(b))
	s := f.do(Options{Paths: []string{a}, Explicit: []string{b}}, cat)
	if s.Mode != "error" || cat.scanned != 0 || !exists(a) || !exists(b) {
		t.Fatalf("%+v", s)
	}
}

// Paths alone never gets the Explicit waivers: an unconsented rule still waits.
func TestPathsDoNotGetExplicitWaivers(t *testing.T) {
	f := newFix(t)
	f.set.ConsentAll = false
	f.set.ConsentedKeys = nil
	a := f.mk("a")
	s := f.do(Options{Paths: []string{a}}, f.cat("temp", core.Auto, f.cand(a)))
	if !exists(a) || len(s.Removed) != 0 {
		t.Fatalf("%+v", s)
	}
	off := f.mk("off")
	s = f.do(Options{Paths: []string{off}}, offCat(depsCand(f, off)))
	if !exists(off) || len(s.Removed) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestExplicitStoppedByBudgetReportsUnseenPaths(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	s := f.do(Options{Explicit: []string{p}, Budget: time.Nanosecond}, f.cat("temp", core.Auto, f.cand(p)))
	if !exists(p) || len(s.Removed) != 0 || !s.Stopped || keptWhy(s.Kept, p) != "budget" {
		t.Fatalf("%+v", s)
	}
}

func TestShowOptionalOffTagsAndRunIgnoresIt(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	cat := offCat(depsCand(f, p))
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
