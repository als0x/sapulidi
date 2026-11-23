package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/guard"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/testutil"
)

// fakeCat returns given candidates and proof results.
type fakeCat struct {
	name    string
	bucket  core.Bucket
	cands   []core.Candidate
	keeps   []core.Kept
	scan    func(env *core.Env) // runs inside Scan, before it returns
	scanned int
	proof   error
	proofFn func(c core.Candidate) error
	facts   map[string]string
}

func (c *fakeCat) Name() string        { return c.name }
func (c *fakeCat) Bucket() core.Bucket { return c.bucket }
func (c *fakeCat) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	c.scanned++
	for k, v := range c.facts {
		env.Facts.Fact(k, v)
	}
	out := append([]core.Candidate(nil), c.cands...)
	if c.scan != nil {
		c.scan(env)
	}
	return out, c.keeps, nil
}
func (c *fakeCat) Proof(env *core.Env, cand core.Candidate) error {
	if c.proofFn != nil {
		return c.proofFn(cand)
	}
	return c.proof
}

// freePlat lets a test change the free space while the run goes on.
type freePlat struct {
	*testutil.FakePlatform
	fn func() uint64
}

func (p *freePlat) FreeBytes(string) (uint64, error) { return p.fn(), nil }

type fix struct {
	t    *testing.T
	home string
	tmp  string
	env  *core.Env
	plat *testutil.FakePlatform
	set  *testutil.FakeSettings
	run  *testutil.FakeRunner
	log  *testutil.MemLogger
	st   *state.Store
}

func resetHooks() {
	beforeRun, afterRun, lowDisk = nil, nil, nil
	beforeSet, afterSet, lowSet = false, false, false
}

func newFix(t *testing.T) *fix {
	t.Helper()
	t.Setenv("SAPULIDI_TEST", "1")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	resetHooks()
	t.Cleanup(resetHooks)
	home := testutil.TempHome(t)
	tmp := filepath.Join(home, "tmp")
	st, err := state.Open(config.Paths{
		Config: filepath.Join(home, ".config", "sapulidi"),
		State:  filepath.Join(home, ".local", "state", "sapulidi"),
		Data:   filepath.Join(home, ".local", "share", "sapulidi"),
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &fix{t: t, home: home, tmp: tmp, st: st,
		plat: &testutil.FakePlatform{Temp: tmp, Free: 1 << 40},
		set:  &testutil.FakeSettings{ConsentAll: true, Categories: map[string]bool{}},
		run:  &testutil.FakeRunner{Paths: map[string]string{}},
		log:  &testutil.MemLogger{},
	}
	f.env = &core.Env{
		Home: home, Now: time.Now(), Platform: f.plat, Run: f.run, Settings: f.set, Log: f.log,
		Ctx: context.Background(), TempDir: tmp,
		Rules: []core.Rule{
			{Pack: "x", ID: "a", Rev: 1, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp, MinAge: time.Hour},
			{Pack: "x", ID: "deps", Rev: 1, Category: "deps", Bucket: core.Optional, Kind: core.KindProject, MinAge: 24 * time.Hour},
			{Pack: "x", ID: "wt", Rev: 1, Category: "worktrees", Bucket: core.Optional, Kind: core.KindPath, MinAge: 24 * time.Hour},
		},
	}
	f.consent("x/a@1")
	return f
}

func (f *fix) consent(keys ...string) {
	f.t.Helper()
	if err := f.st.SaveConsent(&state.Consent{At: time.Now(), Rules: keys}); err != nil {
		f.t.Fatal(err)
	}
}

// mk makes tmp/<name> as an old directory with one old file and returns its path.
func (f *fix) mk(name string) string {
	f.t.Helper()
	p := filepath.Join(f.tmp, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
	file := filepath.Join(p, "data.bin")
	if err := os.WriteFile(file, make([]byte, 5000), 0o644); err != nil {
		f.t.Fatal(err)
	}
	testutil.Touch(f.t, file, 48*time.Hour)
	testutil.Touch(f.t, p, 48*time.Hour)
	return p
}

func (f *fix) cand(path string) core.Candidate {
	return core.Candidate{Category: "temp", RuleKey: "x/a@1", Path: path, Base: f.tmp, Removal: core.RenameDelete, Bytes: -1}
}

func (f *fix) cat(name string, b core.Bucket, cands ...core.Candidate) *fakeCat {
	f.set.Categories[name] = true
	return &fakeCat{name: name, bucket: b, cands: cands}
}

func (f *fix) do(opt Options, cats ...core.Category) Summary {
	f.t.Helper()
	s, err := Run(f.env, cats, f.st, opt)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func hasPath(cs []core.Candidate, p string) bool {
	for _, c := range cs {
		if c.Path == p {
			return true
		}
	}
	return false
}

func keptWhy(ks []core.Kept, p string) string {
	for _, k := range ks {
		if k.Path == p {
			return k.Why
		}
	}
	return ""
}

func TestPlanNeverRemoves(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	s := Plan(f.env, []core.Category{f.cat("temp", core.Auto, f.cand(p))}, Options{})
	if !exists(p) || !hasPath(s.WouldRemove, p) || s.Mode != "dry-run" || len(s.Removed) != 0 {
		t.Fatalf("plan changed or missed: %+v", s)
	}
}

func TestRunNoConsentIsDryRun(t *testing.T) {
	f := newFix(t)
	_ = os.Remove(filepath.Join(f.home, ".local", "state", "sapulidi", "consent.json"))
	p := f.mk("a")
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if s.Mode != "no-consent" || !exists(p) || !hasPath(s.WouldRemove, p) || len(s.Removed) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestNewAutoRuleWaitsForConsent(t *testing.T) {
	f := newFix(t)
	f.set.ConsentAll = false
	f.set.ConsentedKeys = map[string]bool{"x/a@1": true}
	f.consent("x/a@1")
	f.env.Rules = append(f.env.Rules, core.Rule{Pack: "x", ID: "a", Rev: 2, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp, MinAge: time.Hour})
	p := f.mk("a")
	c := f.cand(p)
	c.RuleKey = "x/a@2"
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if !exists(p) || len(s.Removed) != 0 || len(s.Waiting) != 1 || s.Waiting[0] != "x/a@2" {
		t.Fatalf("%+v", s)
	}
}

func TestRunPausedDoesNothing(t *testing.T) {
	f := newFix(t)
	f.st.Pause()
	p := f.mk("a")
	cat := f.cat("temp", core.Auto, f.cand(p))
	s := f.do(Options{}, cat)
	if s.Mode != "paused" || cat.scanned != 0 || !exists(p) {
		t.Fatalf("%+v", s)
	}
}

// A pause that arrives while a run is going stops it before the next removal (spec S11: pause stops every run).
func TestPauseDuringRunStopsRun(t *testing.T) {
	f := newFix(t)
	old := pauseCheckEvery
	pauseCheckEvery = 0
	t.Cleanup(func() { pauseCheckEvery = old })
	a, b := f.mk("a"), f.mk("b")
	cat := f.cat("temp", core.Auto, f.cand(a), f.cand(b))
	cat.proofFn = func(core.Candidate) error {
		f.st.Pause() // the owner types `sapulidi pause` while the run is going
		return nil
	}
	s := f.do(Options{}, cat)
	if len(s.Removed) != 0 || !exists(a) || !exists(b) || !s.Stopped {
		t.Fatalf("a paused run removed or did not stop: %+v", s)
	}
}

func TestPauseCheckIsRateLimited(t *testing.T) {
	f := newFix(t)
	r := newRunner(f.env, Options{}, context.Background(), &Summary{})
	stats := 0
	r.paused = func() bool { stats++; return false }
	for range 50 {
		r.stopped()
	}
	if stats != 1 {
		t.Fatalf("pause file read %d times in a burst, want 1", stats)
	}
}

func TestRunBusyLock(t *testing.T) {
	f := newFix(t)
	rel, err := f.st.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	p := f.mk("a")
	cat := f.cat("temp", core.Auto, f.cand(p))
	s := f.do(Options{}, cat)
	if s.Mode != "busy" || cat.scanned != 0 || !exists(p) {
		t.Fatalf("%+v", s)
	}
}

func TestRunRechecksBeforeRemove(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	cat := f.cat("temp", core.Auto, f.cand(p))
	cat.scan = func(*core.Env) { cat.proof = core.Keep("active", "turned active") }
	s := f.do(Options{}, cat)
	if !exists(p) || keptWhy(s.Kept, p) != "active" {
		t.Fatalf("%+v", s)
	}
}

// The Proof answers nil at the first guard pass and keep after it (the sizing walk is where it changed): the
// re-check right before the rename must ask the Proof again.
func TestRunRechecksProofBeforeRename(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	cat := f.cat("temp", core.Auto, f.cand(p))
	calls := 0
	cat.proofFn = func(core.Candidate) error {
		calls++
		if calls == 1 {
			return nil
		}
		return core.Keep("tracked", "a file became tracked during sizing")
	}
	s := f.do(Options{}, cat)
	if !exists(p) || len(s.Removed) != 0 || keptWhy(s.Kept, p) != "tracked" {
		t.Fatalf("removed after the proof turned to keep: %+v", s)
	}
	if calls != 2 {
		t.Fatalf("proof asked %d times, want 2 (guards, then right before the rename)", calls)
	}
}

func TestRunDenyWinsOverProof(t *testing.T) {
	f := newFix(t)
	ssh := filepath.Join(f.home, ".ssh")
	p := filepath.Join(ssh, "keys")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	c := f.cand(p)
	c.Base = ssh
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if !exists(p) || keptWhy(s.Kept, p) != "deny" {
		t.Fatalf("%+v", s)
	}
}

func TestRunPlaceRejectsSymlinkSwap(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	real := f.mk("real")
	cat := f.cat("temp", core.Auto, f.cand(p))
	cat.scan = func(*core.Env) {
		_ = os.RemoveAll(p)
		_ = os.Symlink(real, p)
	}
	s := f.do(Options{}, cat)
	if !exists(filepath.Join(real, "data.bin")) || keptWhy(s.Kept, p) != "place" {
		t.Fatalf("%+v", s)
	}
}

func TestRunSecretKeeps(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	if err := os.WriteFile(filepath.Join(p, ".env"), []byte("K=v"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, filepath.Join(p, ".env"), 48*time.Hour)
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if !exists(p) || keptWhy(s.Kept, p) != "secret" {
		t.Fatalf("%+v", s)
	}
}

func TestRunOpenKeeps(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	f.plat.Open = map[string]bool{p: true}
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if !exists(p) || keptWhy(s.Kept, p) != "open" {
		t.Fatalf("%+v", s)
	}
}

func TestRunYoungKeeps(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	testutil.Touch(t, filepath.Join(p, "data.bin"), time.Minute)
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if !exists(p) || keptWhy(s.Kept, p) != "young" {
		t.Fatalf("%+v", s)
	}
}

func TestRunMissingRuleKeeps(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	c := f.cand(p)
	c.RuleKey = "x/gone@1"
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if !exists(p) || keptWhy(s.Kept, p) != "error" {
		t.Fatalf("%+v", s)
	}
}

func TestRunRuleDisabledSkipped(t *testing.T) {
	f := newFix(t)
	f.set.DisabledRules = map[string]bool{"x/a@1": true}
	p := f.mk("a")
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if !exists(p) || len(s.Removed)+len(s.WouldRemove)+len(s.Kept) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestRunRemoves(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if exists(p) || !hasPath(s.Removed, p) || s.Mode != "run" || s.FreedBytes <= 0 {
		t.Fatalf("%+v", s)
	}
}

func TestRunRenameCommitPoint(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	var kinds []string
	var renamed string
	for _, e := range f.log.Events {
		if e.Kind == "remove-start" || e.Kind == "remove" {
			kinds = append(kinds, e.Kind)
			if e.Kind == "remove-start" {
				renamed, _ = e.Fields["renamed"].(string)
			}
		}
	}
	if strings.Join(kinds, ",") != "remove-start,remove" {
		t.Fatalf("events %v", kinds)
	}
	if !regexp.MustCompile(`^`+regexp.QuoteMeta(f.tmp)+`/\.sapulidi-removing-[0-9a-f]{16}$`).MatchString(renamed) || exists(renamed) || exists(p) {
		t.Fatalf("renamed %q", renamed)
	}
}

func TestRunFinishesInterruptedRemoval(t *testing.T) {
	f := newFix(t)
	left := filepath.Join(f.tmp, ".sapulidi-removing-x")
	if err := os.MkdirAll(left, 0o755); err != nil {
		t.Fatal(err)
	}
	other := f.mk("other")
	f.do(Options{}, f.cat("temp", core.Auto))
	if exists(left) || !exists(other) {
		t.Fatalf("left=%v other=%v", exists(left), exists(other))
	}
}

func TestRunFinishesLeftoverFromLog(t *testing.T) {
	f := newFix(t)
	deep := filepath.Join(f.tmp, "proj", "deep")
	left := filepath.Join(deep, ".sapulidi-removing-deadbeef")
	if err := os.MkdirAll(left, 0o755); err != nil {
		t.Fatal(err)
	}
	f.st.Logger().Event("remove-start", map[string]any{"path": filepath.Join(deep, "x"), "renamed": left, "parent": deep, "base": f.tmp})
	f.do(Options{}, f.cat("temp", core.Auto))
	if exists(left) {
		t.Fatal("leftover from the log not finished")
	}
}

func TestRunRenameFailureIsFailed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	f := newFix(t)
	p := f.mk("a")
	if err := os.Chmod(f.tmp, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.tmp, 0o755) })
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if !exists(p) || keptWhy(s.Failed, p) != "error" || hasPath(s.Removed, p) {
		t.Fatalf("%+v", s)
	}
}

func TestRunStopsAtBudget(t *testing.T) {
	f := newFix(t)
	cat := f.cat("temp", core.Auto)
	cat.scan = func(env *core.Env) { <-env.Ctx.Done() }
	start := time.Now()
	s := f.do(Options{Budget: 200 * time.Millisecond}, cat)
	if !s.Stopped || time.Since(start) > 1200*time.Millisecond {
		t.Fatalf("stopped=%v after %v", s.Stopped, time.Since(start))
	}
	found := false
	for _, e := range f.log.Events {
		found = found || e.Kind == "budget"
	}
	if !found {
		t.Fatal("no budget event")
	}
}

func TestQuickSkipsSlowCategories(t *testing.T) {
	f := newFix(t)
	var slow []*fakeCat
	for _, n := range []string{"worktrees", "worktree-prune", "containers", "toolchains", "browsers", "caches", "project"} {
		slow = append(slow, f.cat(n, core.Optional)) // project walks every root and runs git per candidate
	}
	p := f.mk("a")
	fast := f.cat("temp", core.Auto, f.cand(p))
	cats := []core.Category{fast}
	for _, c := range slow {
		cats = append(cats, c)
	}
	s := f.do(Options{Quick: true, DryRun: true}, cats...)
	for _, c := range slow {
		if c.scanned != 0 {
			t.Fatalf("%s scanned", c.name)
		}
	}
	if fast.scanned != 1 || len(s.WouldRemove) != 1 || s.WouldRemove[0].Bytes != -1 {
		t.Fatalf("%+v", s)
	}
}

func TestToolCommandUnknownToolRefused(t *testing.T) {
	f := newFix(t)
	f.run.Paths["nosuchtool"] = "/bin/nosuchtool"
	c := core.Candidate{Category: "temp", RuleKey: "x/a@1", Removal: core.ToolCommand, Command: []string{"nosuchtool", "prune"}, Bytes: -1}
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if len(f.run.Calls) != 0 || len(s.Removed) != 0 || len(s.Kept) != 1 || s.Kept[0].Why != "error" {
		t.Fatalf("%+v calls=%v", s, f.run.Calls)
	}
}

func TestToolCommandWithoutPathSkipsPlace(t *testing.T) {
	core.RegisterToolAction("faketool", "prune")
	f := newFix(t)
	f.run.Paths["faketool"] = "/bin/faketool"
	f.run.On([]string{"faketool", "prune"}, "", nil)
	c := core.Candidate{Category: "temp", RuleKey: "x/a@1", Removal: core.ToolCommand, Command: []string{"faketool", "prune"}, Bytes: -1}
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if len(s.Removed) != 1 || len(f.run.Calls) != 1 {
		t.Fatalf("%+v calls=%v", s, f.run.Calls)
	}
}

func TestToolCommandFailureIsFailed(t *testing.T) {
	core.RegisterToolAction("faketool", "prune")
	f := newFix(t)
	f.run.Paths["faketool"] = "/bin/faketool"
	f.run.On([]string{"faketool", "prune"}, "", errors.New("boom"))
	c := core.Candidate{Category: "temp", RuleKey: "x/a@1", Removal: core.ToolCommand, Command: []string{"faketool", "prune"}, Bytes: -1}
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if len(s.Removed) != 0 || len(s.Failed) != 1 {
		t.Fatalf("%+v", s)
	}
}

func worktreeCand(f *fix, p string) core.Candidate {
	return core.Candidate{Category: "worktrees", RuleKey: "x/wt@1", Path: p, Base: f.tmp, Removal: core.WorktreeRemove, Dir: filepath.Join(f.home, "repo"), Bytes: -1}
}

func TestWorktreeRemoveNoForce(t *testing.T) {
	f := newFix(t)
	p := f.mk("wt")
	c := worktreeCand(f, p)
	f.run.On([]string{"git"}, "", nil)
	s := f.do(Options{}, f.cat("worktrees", core.Optional, c))
	if len(s.Removed) != 1 || len(f.run.Calls) == 0 {
		t.Fatalf("%+v", s)
	}
	argv := f.run.Calls[len(f.run.Calls)-1].Argv
	want := guard.GitArgv(c.Dir, "worktree", "remove", "--", p) // hardened: no fsmonitor, hooks or pager; never --force
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv %v", argv)
	}
}

func TestWorktreeWithIgnoredEnvKept(t *testing.T) {
	f := newFix(t)
	p := f.mk("wt")
	if err := os.WriteFile(filepath.Join(p, ".env"), []byte("K=v"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := f.do(Options{}, f.cat("worktrees", core.Optional, worktreeCand(f, p)))
	if keptWhy(s.Kept, p) != "secret" || len(f.run.Calls) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestWorktreeOpenKept(t *testing.T) {
	f := newFix(t)
	p := f.mk("wt")
	f.plat.Open = map[string]bool{p: true}
	s := f.do(Options{}, f.cat("worktrees", core.Optional, worktreeCand(f, p)))
	if keptWhy(s.Kept, p) != "open" || len(f.run.Calls) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestOptionalRunsWithoutRuleConsent(t *testing.T) {
	f := newFix(t)
	f.set.ConsentAll = false
	p := f.mk("a")
	c := f.cand(p)
	c.Category, c.RuleKey = "deps", "x/deps@1"
	s := f.do(Options{}, f.cat("deps", core.Optional, c))
	if exists(p) || !hasPath(s.Removed, p) {
		t.Fatalf("%+v", s)
	}
}

func TestOptionalDisabledNotScanned(t *testing.T) {
	f := newFix(t)
	cat := f.cat("deps", core.Optional)
	f.set.Categories["deps"] = false
	f.do(Options{}, cat)
	if cat.scanned != 0 {
		t.Fatal("disabled category scanned")
	}
}

func TestIgnoreEnabledNeedsPathsAndNeverForNever(t *testing.T) {
	f := newFix(t)
	a, b := f.mk("a"), f.mk("b")
	ca, cb := f.cand(a), f.cand(b)
	ca.Category, cb.Category = "deps", "deps"
	ca.RuleKey, cb.RuleKey = "x/deps@1", "x/deps@1"
	deps := f.cat("deps", core.Optional, ca, cb)
	f.set.Categories["deps"] = false

	f.do(Options{IgnoreEnabled: true}, deps)
	f.do(Options{IgnoreEnabled: true, Paths: []string{a}}, deps) // no Only
	if deps.scanned != 0 {
		t.Fatal("scanned without Paths and Only")
	}
	s := f.do(Options{IgnoreEnabled: true, Paths: []string{a}, Only: []string{"deps"}}, deps)
	if exists(a) || !exists(b) || len(s.Removed) != 1 {
		t.Fatalf("%+v", s)
	}

	never := f.cat("models", core.Never, f.cand(b))
	f.set.Categories["models"] = false
	f.do(Options{IgnoreEnabled: true, Paths: []string{b}, Only: []string{"models"}}, never)
	if never.scanned != 0 || !exists(b) {
		t.Fatal("never category ran")
	}
	// even enabled, a never category is not run
	f.set.Categories["models"] = true
	f.do(Options{}, never)
	if never.scanned != 0 || !exists(b) {
		t.Fatal("enabled never category ran")
	}
}

func TestReportOnlyNeverRemoved(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	c := f.cand(p)
	c.Removal = core.ReportOnly
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if !exists(p) || len(s.Removed)+len(s.WouldRemove) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestNeverBucketRuleNeverRemoved(t *testing.T) {
	f := newFix(t)
	f.env.Rules[0].Bucket = core.Never
	p := f.mk("a")
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if !exists(p) || len(s.Removed)+len(s.WouldRemove) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestFinishChecksPlace(t *testing.T) {
	f := newFix(t)
	real := f.mk("real")
	link := filepath.Join(f.tmp, ".sapulidi-removing-x")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	ks := Finish(f.env, []string{f.tmp})
	if !exists(link) || !exists(filepath.Join(real, "data.bin")) || keptWhy(ks, link) != "place" {
		t.Fatalf("kept=%+v", ks)
	}
}

func lowDiskFix(t *testing.T) (*fix, string, string) {
	f := newFix(t)
	f.set.LowDisk = 100
	a, b := f.mk("la"), f.mk("lb")
	f.env.Platform = &freePlat{f.plat, func() uint64 {
		if !exists(a) {
			return 500
		}
		return 50
	}}
	return f, a, b
}

func TestLowDiskCandidatesGoThroughGuards(t *testing.T) {
	f, a, b := lowDiskFix(t)
	if err := os.WriteFile(filepath.Join(a, ".env"), []byte("K=v"), 0o600); err != nil {
		t.Fatal(err)
	}
	SetLowDisk(func(*core.Env, *Summary) []core.Candidate { return []core.Candidate{f.cand(a), f.cand(b)} })
	s := f.do(Options{}, f.cat("temp", core.Auto))
	if !exists(a) || keptWhy(s.Kept, a) != "secret" || exists(b) || !hasPath(s.Removed, b) {
		t.Fatalf("%+v", s)
	}
}

func TestLowDiskStopsWhenEnough(t *testing.T) {
	f, a, b := lowDiskFix(t)
	SetLowDisk(func(*core.Env, *Summary) []core.Candidate { return []core.Candidate{f.cand(a), f.cand(b)} })
	s := f.do(Options{}, f.cat("temp", core.Auto))
	if exists(a) || !exists(b) || len(s.Removed) != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestFactsNotifiedOnceAndUndeliveredKept(t *testing.T) {
	f := newFix(t)
	f.set.Notify = true
	cat := f.cat("temp", core.Auto)
	cat.facts = map[string]string{"k": "first"}
	f.do(Options{}, cat)
	f.do(Options{}, cat)
	if len(f.plat.Notifications) != 1 {
		t.Fatalf("notifications %v", f.plat.Notifications)
	}
	f.plat.NotifyErr = errors.New("no notifier")
	cat.facts = map[string]string{"k": "second"}
	f.do(Options{}, cat)
	pend := f.st.PendingFacts()
	if len(pend) != 1 || pend[0] != "second" {
		t.Fatalf("pending %v", pend)
	}
}

func TestSummaryLogged(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	n := 0
	for _, e := range f.log.Events {
		if e.Kind == "summary" {
			n++
		}
	}
	if _, ok := f.st.LastRun(); n != 1 || !ok {
		t.Fatalf("summary events %d, lastrun %v", n, ok)
	}
}

func TestHookSetTwicePanics(t *testing.T) {
	resetHooks()
	t.Cleanup(resetHooks)
	check := func(name string, set func()) {
		set()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: second call did not panic", name)
			}
		}()
		set()
	}
	check("before", func() { SetBeforeRun(func(*core.Env, *state.Store, Options) string { return "" }) })
	check("after", func() { SetAfterRun(func(*core.Env, *state.Store, *Summary) {}) })
	check("lowdisk", func() { SetLowDisk(func(*core.Env, *Summary) []core.Candidate { return nil }) })
}

func TestBeforeRunForcesMode(t *testing.T) {
	f := newFix(t)
	SetBeforeRun(func(*core.Env, *state.Store, Options) string { return "uninstalled" })
	p := f.mk("a")
	s := f.do(Options{}, f.cat("temp", core.Auto, f.cand(p)))
	if s.Mode != "uninstalled" || !exists(p) || !hasPath(s.WouldRemove, p) {
		t.Fatalf("%+v", s)
	}
}

func TestAfterRunSeesSummary(t *testing.T) {
	f := newFix(t)
	var got string
	SetAfterRun(func(_ *core.Env, _ *state.Store, s *Summary) { got = s.Mode })
	f.do(Options{}, f.cat("temp", core.Auto))
	if got != "run" {
		t.Fatalf("after hook saw %q", got)
	}
}

func TestSize(t *testing.T) {
	f := newFix(t)
	p := f.mk("a")
	if n := Size(p); n < 5000 {
		t.Fatalf("size %d", n)
	}
	if Size(filepath.Join(f.tmp, "missing")) != -1 {
		t.Fatal("missing should be -1")
	}
}
