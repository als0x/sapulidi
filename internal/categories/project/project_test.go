package project

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	packsload "github.com/als0x/sapulidi/internal/packs"
	"github.com/als0x/sapulidi/internal/testutil"
	"github.com/als0x/sapulidi/packs"
)

const old = 61 * 24 * time.Hour

// allRules loads only the named packs (packs/<name>.toml) from the embedded packs, so a pack of another
// category never makes these tests fail.
func allRules(t *testing.T, only ...string) []core.Rule {
	t.Helper()
	if len(only) == 0 {
		return nil
	}
	sub := fstest.MapFS{}
	for _, name := range only {
		data, err := fs.ReadFile(packs.FS, name+".toml")
		if err != nil {
			t.Fatalf("pack %s: %v", name, err)
		}
		sub[name+".toml"] = &fstest.MapFile{Data: data}
	}
	rules, errs := packsload.Load(sub, "")
	for _, err := range errs {
		t.Fatalf("pack error: %v", err)
	}
	return rules
}

type fixture struct {
	root string
	env  *core.Env
	fp   *testutil.FakePlatform
}

func newFixture(t *testing.T, packNames ...string) *fixture {
	t.Helper()
	t.Setenv("SAPULIDI_TEST", "1")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	home := testutil.TempHome(t)
	root := filepath.Join(home, "work")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fp := &testutil.FakePlatform{Temp: filepath.Join(home, "tmp"), Free: 100 << 30}
	env := &core.Env{
		Home: home, Now: time.Now(), Roots: []core.Root{{Path: root, Source: "config"}},
		Rules: allRules(t, packNames...), Platform: fp, Run: &testutil.FakeRunner{},
		Settings: &testutil.FakeSettings{ConsentAll: true, Categories: map[string]bool{"deps": true, "build-output": true}},
		Log:      &testutil.MemLogger{}, Ctx: context.Background(),
	}
	return &fixture{root: root, env: env, fp: fp}
}

func cat(t *testing.T, name string) core.Category {
	t.Helper()
	for _, c := range core.Categories() {
		if p, ok := c.(*category); ok && p.name == name {
			return p
		}
	}
	t.Fatalf("category %s is not registered", name)
	return nil
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// mkProject writes files (relative paths, content "x") under root/name. With useGit the project is a git
// repository whose first-level directories are all git-ignored and everything else is committed.
func mkProject(t *testing.T, root, name string, useGit bool, files ...string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if useGit {
		testutil.MakeRepo(t, dir)
	}
	var ignore []string
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if first, _, ok := strings.Cut(f, "/"); ok {
			ignore = append(ignore, "/"+first+"/")
		}
	}
	if useGit {
		sort.Strings(ignore)
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(strings.Join(ignore, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-m", "project")
	}
	return dir
}

// ageTree sets the mtime of everything under dir to old.
func ageTree(t *testing.T, dir string) {
	t.Helper()
	mt := time.Now().Add(-old)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		return os.Chtimes(p, mt, mt)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, f *fixture, names ...string) ([]core.Candidate, []core.Kept) {
	t.Helper()
	var cands []core.Candidate
	var kept []core.Kept
	for _, n := range names {
		c, k, err := cat(t, n).Scan(f.env)
		if err != nil {
			t.Fatal(err)
		}
		cands = append(cands, c...)
		kept = append(kept, k...)
	}
	return cands, kept
}

func candPaths(cs []core.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Path)
	}
	sort.Strings(out)
	return out
}

func keptWhy(ks []core.Kept, p string) string {
	for _, k := range ks {
		if k.Path == p {
			return k.Why
		}
	}
	return ""
}

func plan(f *fixture, name string, t *testing.T) engine.Summary {
	return engine.Plan(f.env, []core.Category{cat(t, name)}, engine.Options{})
}

func TestProjectNodeModulesOldIsCandidate(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "node_modules/x/index.js")
	ageTree(t, p)
	cands, kept := scan(t, f, "deps")
	nm := filepath.Join(p, "node_modules")
	if got := candPaths(cands); len(got) != 1 || got[0] != nm {
		t.Fatalf("candidates %v, kept %v", got, kept)
	}
	c := cands[0]
	if c.Base != f.root || c.Removal != core.RenameDelete || c.Category != "deps" || c.RuleKey != "node/node-modules@1" {
		t.Fatalf("candidate %+v", c)
	}
	if !strings.HasPrefix(c.Reason, "node_modules of a project untouched for 59 days") &&
		!strings.HasPrefix(c.Reason, "node_modules of a project untouched for 60 days") {
		t.Fatalf("reason %q", c.Reason)
	}
	if err := cat(t, "deps").Proof(f.env, c); err != nil {
		t.Fatalf("proof: %v", err)
	}
}

// Every git call of the project category (Scan and Proof) is hardened: a repository whose config names a
// core.fsmonitor command must not run it.
func TestProjectGitCallsAreHardened(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "node_modules/x/index.js")
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	script := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, p, "config", "core.fsmonitor", script)
	ageTree(t, p)
	cands, kept := scan(t, f, "deps")
	if len(cands) != 1 {
		t.Fatalf("candidates %v, kept %v", cands, kept)
	}
	if err := cat(t, "deps").Proof(f.env, cands[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the project category ran the repository's core.fsmonitor command")
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

// The file-rule path (crash dumps) asks git itself, not through the guard: it must be hardened too.
func TestProjectFileRuleGitCallsAreHardened(t *testing.T) {
	f := fileRuleFixture(t)
	p := mkProject(t, f.root, "svc", true, "go.mod", "core.123")
	git(t, p, "rm", "--cached", "-q", "core.123")
	if err := os.WriteFile(filepath.Join(p, ".gitignore"), []byte("core.123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, p, "add", ".gitignore")
	git(t, p, "commit", "-m", "ignore")
	ageTree(t, p)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	script := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, p, "config", "core.fsmonitor", script)
	cands, kept := scan(t, f, "crash")
	if len(cands) != 1 {
		t.Fatalf("candidates %v, kept %v", cands, kept)
	}
	if err := cat(t, "crash").Proof(f.env, cands[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the file-rule path ran the repository's core.fsmonitor command")
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

func TestProjectYoungProjectKeepsDeps(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "src.js", "node_modules/x/index.js")
	ageTree(t, p)
	testutil.Touch(t, filepath.Join(p, "src.js"), time.Hour) // the user is working here
	cands, kept := scan(t, f, "deps")
	if len(cands) != 0 || keptWhy(kept, filepath.Join(p, "node_modules")) != "young" {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectYoungArtefactIsKept(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "node_modules/x/index.js")
	ageTree(t, p)
	testutil.Touch(t, filepath.Join(p, "node_modules", "x", "index.js"), time.Hour)
	cands, kept := scan(t, f, "deps")
	if len(cands) != 0 || keptWhy(kept, filepath.Join(p, "node_modules")) != "young" {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectNoDescentIntoNodeModules(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "node_modules/x/package.json",
		"node_modules/x/node_modules/y/index.js", "node_modules/x/dist/out.js")
	ageTree(t, p)
	// Both categories walk the root; neither may propose anything inside node_modules.
	cands, kept := scan(t, f, "deps", "build-output")
	if got := candPaths(cands); len(got) != 1 || got[0] != filepath.Join(p, "node_modules") {
		t.Fatalf("candidates %v, kept %v", got, kept)
	}
	if len(kept) != 0 {
		t.Fatalf("kept %v", kept)
	}
}

func TestProjectNoDescentIntoNodeModulesWhenDepsOff(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "node_modules/x/package.json", "node_modules/x/dist/out.js")
	ageTree(t, p)
	cands, kept := scan(t, f, "build-output") // deps is not scanned, its directories are still not walked
	if len(cands) != 0 || len(kept) != 0 {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectKeepsIgnoredDirWithSecret(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "dist/out.js", "dist/.env")
	ageTree(t, p)
	s := plan(f, "build-output", t)
	dist := filepath.Join(p, "dist")
	if len(s.WouldRemove) != 0 {
		t.Fatalf("would remove %v", s.WouldRemove)
	}
	if why := keptWhy(s.Kept, dist); why != "secret" {
		t.Fatalf("kept %v", s.Kept)
	}
}

func TestProjectKeepsDirWithTrackedFile(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "dist/out.js", "dist/hand-made.js")
	git(t, p, "add", "-f", "dist/hand-made.js")
	git(t, p, "commit", "-m", "track")
	ageTree(t, p)
	cands, kept := scan(t, f, "build-output")
	if len(cands) != 0 || keptWhy(kept, filepath.Join(p, "dist")) != "tracked" {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectKeepsNonGitDir(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "plain", false, "package.json", "node_modules/x/index.js")
	ageTree(t, p)
	cands, kept := scan(t, f, "deps")
	if len(cands) != 0 || keptWhy(kept, filepath.Join(p, "node_modules")) != "not-ignored" {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectKeepsDistWithoutMarker(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "site", true, "index.html", "dist/out.js")
	ageTree(t, p)
	cands, kept := scan(t, f, "build-output")
	if len(cands) != 0 || keptWhy(kept, filepath.Join(p, "dist")) != "no-marker" {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestPythonVenvCandidateCondaKept(t *testing.T) {
	f := newFixture(t, "python")
	p := mkProject(t, f.root, "svc", true, "pyproject.toml", ".venv/pyvenv.cfg", ".venv/lib/x.py",
		"venv/conda-meta/history", "venv/bin/python")
	if err := os.WriteFile(filepath.Join(p, ".venv", "pyvenv.cfg"), []byte("include-system-site-packages = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ageTree(t, p)
	cands, kept := scan(t, f, "deps")
	if got := candPaths(cands); len(got) != 1 || got[0] != filepath.Join(p, ".venv") {
		t.Fatalf("candidates %v, kept %v", got, kept)
	}
	if keptWhy(kept, filepath.Join(p, "venv")) == "" {
		t.Fatalf("conda env not kept: %v", kept)
	}
}

func TestPycacheIsAutoTestArtefact(t *testing.T) {
	f := newFixture(t, "python")
	c := cat(t, "test-artefacts")
	if c.Bucket() != core.Auto {
		t.Fatalf("bucket %s", c.Bucket())
	}
	p := mkProject(t, f.root, "svc", true, "app.py", "__pycache__/app.cpython-312.pyc")
	ageTree(t, p)
	s := plan(f, "test-artefacts", t)
	if len(s.WouldRemove) != 1 || s.WouldRemove[0].Path != filepath.Join(p, "__pycache__") {
		t.Fatalf("would remove %v, kept %v", s.WouldRemove, s.Kept)
	}
}

func TestProjectStopsAtCtx(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "node_modules/x/index.js")
	ageTree(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.env.Ctx = ctx
	cands, kept := scan(t, f, "deps")
	if len(cands) != 0 || len(kept) != 0 {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectSkipsOtherDevice(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "mnt", true, "package.json", "node_modules/x/index.js")
	ageTree(t, p)
	f.fp.OtherDevice = map[string]bool{p: true}
	cands, kept := scan(t, f, "deps")
	if len(cands) != 0 || len(kept) != 0 {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectSkipsWorktreesAndGit(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json")
	wt := mkProject(t, filepath.Join(p, ".worktrees"), "feat", true, "package.json", "node_modules/x/index.js")
	ageTree(t, p)
	_ = wt
	cands, kept := scan(t, f, "deps")
	if len(cands) != 0 || len(kept) != 0 {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectFileRule(t *testing.T) {
	f := newFixture(t)
	f.env.Rules = []core.Rule{{Pack: "x", ID: "dump", Rev: 1, Category: "crash", Bucket: core.Optional, Kind: core.KindFile,
		Names: []string{"core.*"}, Markers: []string{"go.mod"}, MinAge: 14 * 24 * time.Hour}}
	p := mkProject(t, f.root, "svc", true, "go.mod", "core.123", "core.456")
	// core.456 is tracked, core.123 is only ignored.
	git(t, p, "rm", "--cached", "-q", "core.123")
	if err := os.WriteFile(filepath.Join(p, ".gitignore"), []byte("core.123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, p, "add", ".gitignore")
	git(t, p, "commit", "-m", "ignore")
	ageTree(t, p)
	cands, kept := scan(t, f, "crash")
	if got := candPaths(cands); len(got) != 1 || got[0] != filepath.Join(p, "core.123") {
		t.Fatalf("candidates %v, kept %v", got, kept)
	}
	if keptWhy(kept, filepath.Join(p, "core.456")) != "tracked" {
		t.Fatalf("kept %v", kept)
	}
}

func fileRuleFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.env.Rules = []core.Rule{{Pack: "x", ID: "dump", Rev: 1, Category: "crash", Bucket: core.Optional, Kind: core.KindFile,
		Names: []string{"core.*"}, Markers: []string{"go.mod"}, MinAge: 14 * 24 * time.Hour}}
	return f
}

func TestProjectFileRuleKeepsLinkedWorktree(t *testing.T) {
	f := fileRuleFixture(t)
	p := mkProject(t, f.root, "svc", true, "go.mod", "core.1")
	git(t, p, "rm", "--cached", "-q", "core.1")
	if err := os.WriteFile(filepath.Join(p, ".gitignore"), []byte("core.*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, p, "add", ".gitignore")
	git(t, p, "commit", "-m", "ignore")
	wt := filepath.Join(f.root, "wt")
	git(t, p, "worktree", "add", "-q", wt)
	testutil.Touch(t, filepath.Join(wt, "core.2"), old)
	ageTree(t, p)
	ageTree(t, wt)
	cands, kept := scan(t, f, "crash")
	if got := candPaths(cands); len(got) != 1 || got[0] != filepath.Join(p, "core.1") {
		t.Fatalf("candidates %v, kept %v", got, kept)
	}
	if keptWhy(kept, filepath.Join(wt, "core.2")) != "not-ignored" {
		t.Fatalf("kept %v", kept)
	}
}

func TestProjectFileRuleKeepsBareRepoAndUnignored(t *testing.T) {
	f := fileRuleFixture(t)
	bare := filepath.Join(f.root, "bare")
	for _, rel := range []string{"HEAD", "objects/x", "go.mod", "core.1"} {
		testutil.Touch(t, filepath.Join(bare, rel), old)
	}
	p := mkProject(t, f.root, "svc", true, "go.mod", "core.1")
	git(t, p, "rm", "--cached", "-q", "core.1") // present, untracked, not ignored
	git(t, p, "commit", "-m", "untrack")
	nomarker := mkProject(t, f.root, "other", true, "core.1")
	git(t, nomarker, "rm", "--cached", "-q", "core.1")
	if err := os.WriteFile(filepath.Join(nomarker, ".gitignore"), []byte("core.*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ageTree(t, bare)
	ageTree(t, p)
	ageTree(t, nomarker)
	cands, kept := scan(t, f, "crash")
	if len(cands) != 0 {
		t.Fatalf("candidates %v", candPaths(cands))
	}
	for p, why := range map[string]string{
		filepath.Join(bare, "core.1"):     "nested-git",
		filepath.Join(p, "core.1"):        "not-ignored",
		filepath.Join(nomarker, "core.1"): "no-marker",
	} {
		if got := keptWhy(kept, p); got != why {
			t.Errorf("%s: kept %q, want %q (%v)", p, got, why, kept)
		}
	}
}

func TestProjectNewestCountsSourceNamedLikeArtefacts(t *testing.T) {
	f := newFixture(t, "node")
	p := mkProject(t, f.root, "app", true, "package.json", "src/build/a.js", "src/dist/b.js", "lib/coverage/c.js",
		"node_modules/x/index.js")
	ageTree(t, p)
	// The user adds a file to source: the directory (depth 2 of the marker directory) changes.
	testutil.Touch(t, filepath.Join(p, "lib", "coverage"), time.Hour)
	cands, kept := scan(t, f, "deps")
	if len(cands) != 0 || keptWhy(kept, filepath.Join(p, "node_modules")) != "young" {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectNoOfferInsideAnotherCandidate(t *testing.T) {
	f := newFixture(t, "node", "python", "test-artefacts")
	p := mkProject(t, f.root, "svc", true, "pyproject.toml", "build/lib/mod.py", "build/lib/__pycache__/mod.pyc",
		"build/coverage/x")
	ageTree(t, p)
	cands, kept := scan(t, f, "test-artefacts")
	if len(cands) != 0 || len(kept) != 0 {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestProjectVenvNeedsPyvenvCfg(t *testing.T) {
	f := newFixture(t, "python")
	p := mkProject(t, f.root, "svc", true, "pyproject.toml", "venv/data.csv", ".venv/pyvenv.cfg")
	ageTree(t, p)
	cands, kept := scan(t, f, "deps")
	if got := candPaths(cands); len(got) != 1 || got[0] != filepath.Join(p, ".venv") {
		t.Fatalf("candidates %v, kept %v", got, kept)
	}
	if keptWhy(kept, filepath.Join(p, "venv")) != "no-marker" {
		t.Fatalf("kept %v", kept)
	}
}

func TestProjectEvaluateStopsAtCtx(t *testing.T) {
	f := newFixture(t, "node")
	ctx, cancel := context.WithCancel(context.Background())
	f.env.Ctx = ctx
	cancel()
	var rule core.Rule
	for _, r := range f.env.Rules {
		if r.ID == "node-modules" {
			rule = r
		}
	}
	if _, err := cat(t, "deps").(*category).evaluate(f.env, rule, filepath.Join(f.root, "nope")); err == nil ||
		keptOf(rule, "x", err).Why != "budget" {
		t.Fatalf("evaluate with a done ctx: %v", err)
	}
}

// TestPackFixtures scans every fixture project under testdata/<pack>/ with the pack's own rules and compares the
// outcome with testdata/<pack>/expect.txt. A top-level directory called nogit-* is not a repository; every
// other one is, with all its first-level directories git-ignored. Lines: "candidate <path>" or "keep <path> <why>".
func TestPackFixtures(t *testing.T) {
	packDirs, err := filepath.Glob("testdata/*")
	if err != nil || len(packDirs) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, packDir := range packDirs {
		pack := filepath.Base(packDir)
		t.Run(pack, func(t *testing.T) {
			f := newFixture(t, pack)
			data, err := os.ReadFile(filepath.Join(packDir, "expect.txt"))
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				kind, rest, _ := strings.Cut(line, " ")
				if kind == "candidate" {
					want[rest] = "candidate"
				} else {
					p, why, _ := strings.Cut(rest, " ")
					want[p] = "keep " + why
				}
			}
			projects, _ := os.ReadDir(packDir)
			for _, pr := range projects {
				if !pr.IsDir() {
					continue
				}
				var files []string
				src := filepath.Join(packDir, pr.Name())
				_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
					if err == nil && !d.IsDir() {
						rel, _ := filepath.Rel(src, p)
						files = append(files, filepath.ToSlash(rel))
					}
					return nil
				})
				// The artefact directories named by expect.txt are generated, not stored: the repository's own
				// .gitignore (dist/) would swallow some of them.
				// A path that is a stored file is a file candidate or keep: it is not generated. Files in a
				// first-level directory are git-ignored and untracked; top-level files are tracked.
				for p := range want {
					if rel, ok := strings.CutPrefix(p, pr.Name()+"/"); ok {
						if fi, err := os.Lstat(filepath.Join(src, rel)); err == nil && fi.Mode().IsRegular() {
							continue
						}
						files = append(files, rel+"/artefact.txt")
					}
				}
				dir := mkProject(t, f.root, pr.Name(), !strings.HasPrefix(pr.Name(), "nogit-"), files...)
				for _, rel := range files { // keep the fixture's own content
					if b, err := os.ReadFile(filepath.Join(src, rel)); err == nil {
						if err := os.WriteFile(filepath.Join(dir, rel), b, 0o644); err != nil {
							t.Fatal(err)
						}
					}
				}
				// tracked.txt lists relative paths (one per line) that the project tracks although they sit in a
				// git-ignored directory.
				if b, err := os.ReadFile(filepath.Join(src, "tracked.txt")); err == nil && !strings.HasPrefix(pr.Name(), "nogit-") {
					for _, rel := range strings.Fields(string(b)) {
						git(t, dir, "add", "-f", "--", rel)
					}
					git(t, dir, "commit", "-m", "tracked files")
				}
				ageTree(t, dir)
			}
			cands, kept := scan(t, f, Categories...)
			got := map[string]string{}
			for _, c := range cands {
				rel, _ := filepath.Rel(f.root, c.Path)
				got[filepath.ToSlash(rel)] = "candidate"
			}
			for _, k := range kept {
				rel, _ := filepath.Rel(f.root, k.Path)
				got[filepath.ToSlash(rel)] = "keep " + k.Why
			}
			for p, w := range want {
				if got[p] != w {
					t.Errorf("%s: want %q, got %q", p, w, got[p])
				}
			}
			for p, g := range got {
				if _, ok := want[p]; !ok {
					t.Errorf("%s: unexpected %q", p, g)
				}
			}
		})
	}
}
