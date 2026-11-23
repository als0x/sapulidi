package caches

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	packsload "github.com/als0x/sapulidi/internal/packs"
	"github.com/als0x/sapulidi/internal/testutil"
	"github.com/als0x/sapulidi/packs"
)

// loadRules loads only packs/caches.toml, never the whole embedded set.
func loadRules(t *testing.T) []core.Rule {
	t.Helper()
	data, err := packs.FS.ReadFile("caches.toml")
	if err != nil {
		t.Fatal(err)
	}
	rules, errs := packsload.Load(fstest.MapFS{"caches.toml": &fstest.MapFile{Data: data}}, "")
	for _, e := range errs {
		t.Fatalf("pack error: %v", e)
	}
	return rules
}

type fixture struct {
	home string
	fr   *testutil.FakeRunner
	set  *testutil.FakeSettings
	env  *core.Env
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("SAPULIDI_TEST", "1")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	home := testutil.TempHome(t)
	fr := &testutil.FakeRunner{Paths: map[string]string{}}
	set := &testutil.FakeSettings{Categories: map[string]bool{"caches": true, "mcp-runtime": true}}
	env := &core.Env{
		Exclusions: testutil.NoExclusions,
		Home:       home, Now: time.Now(), Rules: loadRules(t), Platform: &testutil.FakePlatform{Free: 100 << 30},
		Run: fr, Settings: set, Log: &testutil.MemLogger{}, Ctx: context.Background(),
	}
	return &fixture{home: home, fr: fr, set: set, env: env}
}

// tool puts id on PATH and scripts its cache dir query; the dir gets one file of size bytes.
func (f *fixture) tool(t *testing.T, id string, query []string, dir string, size int) {
	t.Helper()
	f.fr.Paths[id] = "/fake/bin/" + id
	if query != nil {
		f.fr.On(query, dir+"\n", nil)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) dir(name string) string { return filepath.Join(f.home, ".cache", name) }

func keys(cs []core.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.RuleKey)
	}
	return out
}

func TestToolSkippedWhenMissing(t *testing.T) {
	f := newFixture(t)
	if got := lowDiskCandidates(f.env); len(got) != 0 {
		t.Fatalf("no tool on PATH, got %v", keys(got))
	}
	for _, c := range f.fr.Calls {
		t.Errorf("a missing tool must not be run: %v", c.Argv)
	}
}

func TestCandidatesUseOfficialCommand(t *testing.T) {
	f := newFixture(t)
	f.tool(t, "npm", []string{"npm", "config", "get", "cache"}, f.dir("npm"), 10)
	got := lowDiskCandidates(f.env)
	if len(got) != 1 {
		t.Fatalf("want one candidate, got %v", keys(got))
	}
	c := got[0]
	if c.Category != "caches" || c.RuleKey != "caches/npm-clean-cache@1" || c.Removal != core.ToolCommand ||
		c.Path != f.dir("npm") || c.Base != filepath.Dir(f.dir("npm")) ||
		!slices.Equal(c.Command, []string{"npm", "cache", "clean", "--force"}) {
		t.Fatalf("candidate = %+v", c)
	}
}

func TestLowDiskReturnsLargestFirst(t *testing.T) {
	f := newFixture(t)
	f.tool(t, "go", []string{"go", "env", "GOCACHE"}, f.dir("go"), 4096)
	f.tool(t, "npm", []string{"npm", "config", "get", "cache"}, f.dir("npm"), 3*4096*16)
	f.tool(t, "pip", []string{"pip", "cache", "dir"}, f.dir("pip"), 4*4096)
	got := keys(lowDiskCandidates(f.env))
	want := []string{"caches/npm-clean-cache@1", "caches/pip-purge@1", "caches/go-clean-cache@1"}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestLowDiskOnlyEnabled(t *testing.T) {
	f := newFixture(t)
	f.tool(t, "go", []string{"go", "env", "GOCACHE"}, f.dir("go"), 10)
	f.tool(t, "npm", []string{"npm", "config", "get", "cache"}, f.dir("npm"), 10)
	f.tool(t, "uv", []string{"uv", "cache", "dir"}, f.dir("uv"), 10) // mcp-runtime, never in the low-disk tier
	f.set.DisabledRules = map[string]bool{"caches/go-clean-cache@1": true}
	if got := keys(lowDiskCandidates(f.env)); !slices.Equal(got, []string{"caches/npm-clean-cache@1"}) {
		t.Fatalf("disabled rule or other category leaked: %v", got)
	}
	f.set.Categories["caches"] = false
	if got := lowDiskCandidates(f.env); len(got) != 0 {
		t.Fatalf("disabled category: %v", keys(got))
	}
}

func TestReportOnlyLocationsRegistered(t *testing.T) {
	for _, id := range []string{"npm-npx", "gradle-caches", "maven-repo", "cargo-registry", "cargo-git", "deno-cache",
		"gradle-daemon-logs"} {
		if !core.LocationKnown(id) {
			t.Errorf("location %s is not registered", id)
		}
	}
}

func TestEveryPackRuleResolves(t *testing.T) {
	for _, r := range loadRules(t) {
		if r.Kind == core.KindCache && !core.ToolActionKnown(r.Tool, r.Action) {
			t.Errorf("%s: unknown %s/%s", r.Key(), r.Tool, r.Action)
		}
	}
}

func TestNpxReportOnly(t *testing.T) {
	f := newFixture(t)
	f.tool(t, "npm", []string{"npm", "config", "get", "cache"}, f.dir("npm"), 10)
	npx := filepath.Join(f.dir("npm"), "_npx")
	if err := os.MkdirAll(npx, 0o755); err != nil {
		t.Fatal(err)
	}
	cands, _, err := (&category{name: "caches"}).Scan(f.env)
	if err != nil {
		t.Fatal(err)
	}
	var found *core.Candidate
	for i, c := range cands {
		if c.RuleKey == "caches/npm-npx@1" {
			found = &cands[i]
		}
	}
	if found == nil || found.Removal != core.ReportOnly || found.Path != npx {
		t.Fatalf("npx candidate = %+v in %v", found, keys(cands))
	}
	if found.Reason == "" {
		t.Error("the reason must say npm has no command for _npx alone")
	}
	s := engine.Plan(f.env, []core.Category{&category{name: "caches"}}, engine.Options{})
	if len(s.WouldRemove) != 0 || len(s.Removed) != 0 {
		t.Fatalf("report-only must never be removed: %+v", s)
	}
}

func TestReportOnlyCachesNeverRun(t *testing.T) {
	f := newFixture(t)
	for _, d := range []string{".gradle/caches", ".m2/repository", ".cargo/registry", ".cargo/git"} {
		if err := os.MkdirAll(filepath.Join(f.home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cands, _, err := (&category{name: "caches"}).Scan(f.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 4 {
		t.Fatalf("want 4 report-only candidates, got %v", keys(cands))
	}
	for _, c := range cands {
		if c.Removal != core.ReportOnly {
			t.Errorf("%s is %s, want report-only", c.RuleKey, c.Removal)
		}
	}
	s := engine.Plan(f.env, []core.Category{&category{name: "caches"}}, engine.Options{})
	if len(s.WouldRemove) != 0 || len(f.fr.Calls) != 0 {
		t.Fatalf("nothing may run or be planned: %+v, calls %v", s.WouldRemove, f.fr.Calls)
	}
}

func TestGradleDaemonLogsOld(t *testing.T) {
	f := newFixture(t)
	d := filepath.Join(f.home, ".gradle", "daemon", "8.5")
	oldLog, newLog, other := filepath.Join(d, "a.log"), filepath.Join(d, "b.log"), filepath.Join(d, "c.txt")
	testutil.Touch(t, oldLog, 10*24*time.Hour)
	testutil.Touch(t, newLog, time.Hour)
	testutil.Touch(t, other, 10*24*time.Hour)
	cat := &category{name: "caches"}
	cands, _, err := cat.Scan(f.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Path != oldLog || cands[0].Removal != core.RenameDelete || cands[0].Base != d {
		t.Fatalf("candidates = %+v", cands)
	}
	if err := cat.Proof(f.env, cands[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
	s := engine.Plan(f.env, []core.Category{cat}, engine.Options{})
	if len(s.WouldRemove) != 1 || s.WouldRemove[0].Path != oldLog {
		t.Fatalf("plan = %+v kept %+v", s.WouldRemove, s.Kept)
	}
}

func TestWeeklyPruneOncePer7Days(t *testing.T) {
	f := newFixture(t)
	f.tool(t, "uv", []string{"uv", "cache", "dir"}, f.dir("uv"), 10)
	cat := &category{name: "mcp-runtime"}
	cands, _, err := cat.Scan(f.env)
	if err != nil || len(cands) != 1 || cands[0].RuleKey != "caches/uv-prune@1" ||
		!slices.Equal(cands[0].Command, []string{"uv", "cache", "prune"}) {
		t.Fatalf("first scan: %v %v", keys(cands), err)
	}
	if err := cat.Proof(f.env, cands[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
	if again, _, _ := cat.Scan(f.env); len(again) != 1 {
		t.Fatalf("Proof must be pure, a scan after it still proposes: %v", keys(again))
	}
	cat.AfterRemove(f.env, cands[0]) // the engine calls this after a removal that went through
	if again, _, _ := cat.Scan(f.env); len(again) != 0 {
		t.Fatalf("second scan the same day: %v", keys(again))
	}
	f.env.Now = f.env.Now.Add(6*24*time.Hour + 23*time.Hour)
	if again, _, _ := cat.Scan(f.env); len(again) != 0 {
		t.Fatalf("scan before 7 days: %v", keys(again))
	}
	f.env.Now = f.env.Now.Add(2 * time.Hour)
	if again, _, _ := cat.Scan(f.env); len(again) != 1 {
		t.Fatalf("scan after 7 days: %v", keys(again))
	}
}

func TestProofRejectsForeignCommand(t *testing.T) {
	f := newFixture(t)
	f.tool(t, "npm", []string{"npm", "config", "get", "cache"}, f.dir("npm"), 10)
	c := lowDiskCandidates(f.env)[0]
	cat := &category{name: "caches"}
	if err := cat.Proof(f.env, c); err != nil {
		t.Fatalf("good candidate: %v", err)
	}
	c.Command = []string{"npm", "exec", "evil"}
	if err := cat.Proof(f.env, c); err == nil {
		t.Fatal("a command that is not the rule's action must be kept")
	}
}

func TestCachesScanProposesNoToolCommand(t *testing.T) {
	f := newFixture(t)
	f.tool(t, "go", []string{"go", "env", "GOCACHE"}, f.dir("go"), 10)
	cands, _, _ := (&category{name: "caches"}).Scan(f.env)
	for _, c := range cands {
		if c.Removal == core.ToolCommand {
			t.Fatalf("caches run only in the low-disk tier, scan proposed %s", c.RuleKey)
		}
	}
}
