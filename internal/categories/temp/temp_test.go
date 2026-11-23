package temp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/testutil"
)

func testRules() []core.Rule {
	return []core.Rule{
		{Pack: "temp", ID: "go-build", Rev: 1, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp,
			Names: []string{"go-build*"}, MinAge: time.Hour},
		{Pack: "temp", ID: "pytest", Rev: 1, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp,
			Names: []string{"pytest-of-*"}, MinAge: 2 * time.Hour},
	}
}

type fixture struct {
	tmp string
	fp  *testutil.FakePlatform
	env *core.Env
	cat *category
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("SAPULIDI_TEST", "1")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	home := testutil.TempHome(t)
	tmp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	fp := &testutil.FakePlatform{Temp: tmp, Free: 100 << 30}
	env := &core.Env{
		Home: home, Now: time.Now(), Rules: testRules(), Platform: fp, Run: &testutil.FakeRunner{},
		Settings: &testutil.FakeSettings{ConsentAll: true}, Log: &testutil.MemLogger{}, Ctx: context.Background(),
		TempDir: tmp,
	}
	return &fixture{tmp: tmp, fp: fp, env: env, cat: &category{}}
}

// mkdir makes dir with one file inside and sets the mtime of both to age ago.
func mkdir(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	testutil.Touch(t, filepath.Join(dir, "file"), age)
	testutil.Touch(t, dir, age)
}

func (f *fixture) plan() engine.Summary {
	return engine.Plan(f.env, []core.Category{f.cat}, engine.Options{})
}

func wouldRemove(s engine.Summary, path string) bool {
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

func TestTempFindsOldGoBuild(t *testing.T) {
	f := newFixture(t)
	old := filepath.Join(f.tmp, "go-build123")
	mkdir(t, old, 3*time.Hour)
	other := filepath.Join(f.tmp, "unrelated")
	mkdir(t, other, 30*time.Hour)
	s := f.plan()
	if !wouldRemove(s, old) {
		t.Fatalf("old go-build not proposed: %+v", s)
	}
	if wouldRemove(s, other) || keptWhy(s, other) != "" {
		t.Fatalf("an entry no rule names was touched: %+v", s)
	}
	if got := s.WouldRemove[0].RuleKey; got != "temp/go-build@1" {
		t.Fatalf("rule key %q", got)
	}
}

func TestTempIgnoresFilesWithRuleNames(t *testing.T) {
	f := newFixture(t)
	f.env.Rules = append(f.env.Rules,
		core.Rule{Pack: "temp", ID: "pip", Rev: 1, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp,
			Names: []string{"pip-*"}, MinAge: 2 * time.Hour},
		core.Rule{Pack: "temp", ID: "vitest", Rev: 1, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp,
			Names: []string{"vitest*"}, MinAge: 2 * time.Hour},
		core.Rule{Pack: "temp", ID: "jest", Rev: 1, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp,
			Names: []string{"jest_*"}, MinAge: 2 * time.Hour})
	var files, dirs []string
	for _, n := range []string{"pip-freeze.txt", "vitest.config.ts", "jest_cache.json"} {
		p := filepath.Join(f.tmp, n)
		testutil.Touch(t, p, 5*time.Hour)
		files = append(files, p)
	}
	for _, n := range []string{"pip-123", "vitest-abc", "jest_xyz"} {
		p := filepath.Join(f.tmp, n)
		mkdir(t, p, 5*time.Hour)
		dirs = append(dirs, p)
	}
	s := f.plan()
	for _, p := range files {
		if wouldRemove(s, p) || keptWhy(s, p) != "" {
			t.Errorf("file %s is a candidate: %+v", p, s)
		}
	}
	c := core.Candidate{Category: "temp", RuleKey: "temp/pip@1", Path: files[0], Base: f.tmp, Removal: core.RenameDelete}
	if f.cat.Proof(f.env, c) == nil {
		t.Errorf("Proof accepted a file")
	}
	for _, p := range dirs {
		if !wouldRemove(s, p) {
			t.Errorf("directory %s not proposed: %+v", p, s)
		}
	}
}

func TestTempKeepsYoung(t *testing.T) {
	f := newFixture(t)
	young := filepath.Join(f.tmp, "go-build-new")
	mkdir(t, young, 5*time.Minute)
	// older than go-build's 1 h but younger than pytest's 2 h: each rule has its own age
	mid := filepath.Join(f.tmp, "pytest-of-me")
	mkdir(t, mid, 90*time.Minute)
	s := f.plan()
	if wouldRemove(s, young) || keptWhy(s, young) != "young" {
		t.Fatalf("young go-build: %+v", s)
	}
	if wouldRemove(s, mid) || keptWhy(s, mid) != "young" {
		t.Fatalf("pytest at 90 min: %+v", s)
	}
}

func TestTempKeepsForeignOwned(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.tmp, "go-build-theirs")
	mkdir(t, p, 5*time.Hour)
	f.fp.NotOwned = map[string]bool{p: true}
	s := f.plan()
	if wouldRemove(s, p) {
		t.Fatalf("foreign entry proposed: %+v", s)
	}
	// Proof alone also refuses it, in case ownership changes after the scan.
	c := core.Candidate{Category: "temp", RuleKey: "temp/go-build@1", Path: p, Base: f.tmp, Removal: core.RenameDelete}
	if err := f.cat.Proof(f.env, c); err == nil {
		t.Fatal("Proof accepted a foreign entry")
	}
}

func TestTempSkipsSymlink(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(f.env.Home, "precious")
	mkdir(t, target, 50*time.Hour)
	link := filepath.Join(f.tmp, "go-build-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	s := f.plan()
	if wouldRemove(s, link) || wouldRemove(s, target) {
		t.Fatalf("symlink proposed: %+v", s)
	}
	c := core.Candidate{Category: "temp", RuleKey: "temp/go-build@1", Path: link, Base: f.tmp, Removal: core.RenameDelete}
	if err := f.cat.Proof(f.env, c); err == nil {
		t.Fatal("Proof accepted a symlink")
	}
}

func TestTempKeepsArgsMentioned(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.tmp, "go-build-busy")
	mkdir(t, p, 5*time.Hour)
	f.fp.Mentioned = map[string]bool{p: true}
	s := f.plan()
	if wouldRemove(s, p) || keptWhy(s, p) != "process" {
		t.Fatalf("entry named in a live process's arguments: %+v", s)
	}
}

func TestTempProofRechecksName(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.tmp, "something-else")
	mkdir(t, p, 5*time.Hour)
	c := core.Candidate{Category: "temp", RuleKey: "temp/go-build@1", Path: p, Base: f.tmp, Removal: core.RenameDelete}
	if err := f.cat.Proof(f.env, c); err == nil {
		t.Fatal("Proof accepted a name the rule does not match")
	}
	c.Path = filepath.Join(f.tmp, "sub", "go-build1")
	if err := f.cat.Proof(f.env, c); err == nil {
		t.Fatal("Proof accepted a nested path")
	}
}

func vitestRule() core.Rule {
	return core.Rule{Pack: "temp", ID: "vitest", Rev: 1, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp,
		Names: []string{"vitest*"}, MinAge: 2 * time.Hour}
}

// repoInTemp makes tmp/vitest-repro holding a .git directory and a file, both aged 3 h.
func repoInTemp(t *testing.T, tmp string) string {
	t.Helper()
	repo := filepath.Join(tmp, "vitest-repro")
	testutil.Touch(t, filepath.Join(repo, ".git", "HEAD"), 3*time.Hour)
	testutil.Touch(t, filepath.Join(repo, ".git"), 3*time.Hour)
	testutil.Touch(t, filepath.Join(repo, "edit.txt"), 3*time.Hour)
	testutil.Touch(t, repo, 3*time.Hour)
	return repo
}

func TestTempKeepsGitRepo(t *testing.T) {
	f := newFixture(t)
	f.env.Rules = append(f.env.Rules, vitestRule())
	repo := repoInTemp(t, f.tmp)
	s := f.plan()
	if wouldRemove(s, repo) || keptWhy(s, repo) != "nested-git" {
		t.Fatalf("repository in temp: %+v", s)
	}
	// a bare repository one level down counts too
	bare := filepath.Join(f.tmp, "vitest-bare")
	testutil.Touch(t, filepath.Join(bare, "sub.git", "HEAD"), 3*time.Hour)
	testutil.Touch(t, filepath.Join(bare, "sub.git", "objects", "x"), 3*time.Hour)
	testutil.Touch(t, filepath.Join(bare, "sub.git", "objects"), 3*time.Hour)
	testutil.Touch(t, filepath.Join(bare, "sub.git"), 3*time.Hour)
	testutil.Touch(t, bare, 3*time.Hour)
	s = f.plan()
	if wouldRemove(s, bare) || keptWhy(s, bare) != "nested-git" {
		t.Fatalf("bare repository in temp: %+v", s)
	}
	// Proof alone refuses it, in case a repository appears after the scan
	c := core.Candidate{Category: "temp", RuleKey: "temp/vitest@1", Path: repo, Base: f.tmp, Removal: core.RenameDelete}
	if err := f.cat.Proof(f.env, c); err == nil {
		t.Fatal("Proof accepted a repository")
	}
}

// The walk for a repository is for entries old enough to be removed: a young one with a repository inside is kept
// as young by the freshness guard, and was never walked.
func TestTempWalksOnlyOldEnoughEntries(t *testing.T) {
	f := newFixture(t)
	f.env.Rules = append(f.env.Rules, vitestRule())
	young := filepath.Join(f.tmp, "vitest-young")
	testutil.Touch(t, filepath.Join(young, ".git", "HEAD"), 5*time.Minute)
	testutil.Touch(t, young, 5*time.Minute)
	s := f.plan()
	if wouldRemove(s, young) || keptWhy(s, young) != "young" {
		t.Fatalf("young entry: %+v", s)
	}
}

func TestTempCancelledWalkKeeps(t *testing.T) {
	f := newFixture(t)
	f.env.Rules = append(f.env.Rules, vitestRule())
	p := filepath.Join(f.tmp, "vitest-plain")
	mkdir(t, p, 3*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := core.Candidate{Category: "temp", RuleKey: "temp/vitest@1", Path: p, Base: f.tmp, Removal: core.RenameDelete}
	f.env.Ctx = ctx
	if err := f.cat.Proof(f.env, c); err == nil {
		t.Fatal("Proof accepted an entry it could not walk")
	}
}

func TestRunKeepsRepoInTempDir(t *testing.T) {
	f := newFixture(t)
	f.env.Rules = append(f.env.Rules, vitestRule())
	f.env.Settings = &testutil.FakeSettings{ConsentAll: true, Categories: map[string]bool{"temp": true}}
	repo := repoInTemp(t, f.tmp)
	st, err := state.Open(config.Paths{
		Config: filepath.Join(f.env.Home, ".config", "sapulidi"),
		State:  filepath.Join(f.env.Home, ".local", "state", "sapulidi"),
		Data:   filepath.Join(f.env.Home, ".local", "share", "sapulidi"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveConsent(&state.Consent{At: time.Now(), Rules: []string{"temp/vitest@1"}}); err != nil {
		t.Fatal(err)
	}
	s, err := engine.Run(f.env, []core.Category{f.cat}, st, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != "run" || len(s.Removed) != 0 {
		t.Fatalf("run removed something: %+v", s)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("repository gone: %v", err)
	}
}

func TestTempScansExtraBase(t *testing.T) {
	f := newFixture(t)
	extra := filepath.Join(f.env.Home, "othertmp")
	mkdir(t, filepath.Join(extra, "go-build9"), 3*time.Hour)
	f.cat = &category{extra: func(*core.Env) []string { return []string{extra, f.tmp} }}
	cands, _, err := f.cat.Scan(f.env)
	if err != nil || len(cands) != 1 || cands[0].Base != extra {
		t.Fatalf("cands %+v err %v", cands, err)
	}
}

func TestTempDefaultExtraIsSilentUnderTestFlag(t *testing.T) {
	t.Setenv("SAPULIDI_TEST", "1")
	env := &core.Env{Platform: &testutil.FakePlatform{OSName: "linux"}}
	if got := linuxTmp(env); got != nil {
		t.Fatalf("test flag still scans %v", got)
	}
	t.Setenv("SAPULIDI_TEST", "")
	if got := linuxTmp(env); len(got) != 1 || got[0] != "/tmp" {
		t.Fatalf("linux extra base %v", got)
	}
	env.Platform = &testutil.FakePlatform{OSName: "darwin"}
	if got := linuxTmp(env); got != nil {
		t.Fatalf("darwin extra base %v", got)
	}
}
