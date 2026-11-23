package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

const (
	day = 24 * time.Hour
	id1 = "11111111-1111-1111-1111-111111111111"
	id2 = "22222222-2222-2222-2222-222222222222"
)

func pathRule(cat string, b core.Bucket, loc string, age time.Duration) core.Rule {
	return core.Rule{Pack: "agent", ID: loc, Rev: 1, Category: cat, Bucket: b, Kind: core.KindPath, MinAge: age, Location: loc}
}

func newEnv(home string, plat *testutil.FakePlatform, rules ...core.Rule) *core.Env {
	return &core.Env{
		Home: home, Now: time.Now(), Rules: rules, Platform: plat, Run: &testutil.FakeRunner{},
		Settings: &testutil.FakeSettings{}, Ctx: context.Background(), TempDir: filepath.Join(home, "tmp"),
	}
}

func category(t *testing.T, name string) core.Category {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // a developer's own setting must not leak into a test
	for _, c := range core.Categories() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("category %s not registered", name)
	return nil
}

func scan(t *testing.T, name string, e *core.Env) ([]core.Candidate, []core.Kept) {
	t.Helper()
	c, k, err := category(t, name).Scan(e)
	if err != nil {
		t.Fatal(err)
	}
	return c, k
}

func mkdir(t *testing.T, p string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, p, age)
	return p
}

func write(t *testing.T, p, content string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, p, age)
	return p
}

func paths(cs []core.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}

func wantPaths(t *testing.T, got []core.Candidate, want ...string) {
	t.Helper()
	g := paths(got)
	if len(g) != len(want) {
		t.Fatalf("candidates = %v, want %v", g, want)
	}
	seen := map[string]bool{}
	for _, p := range g {
		seen[p] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Fatalf("candidates = %v, want %v", g, want)
		}
	}
}

func scratchEnv(home, base string) *core.Env {
	return newEnv(home, &testutil.FakePlatform{Scratch: []string{base}},
		pathRule("agent-scratch", core.Auto, "claude-scratch", day),
		pathRule("agent-scratch", core.Auto, "codex-tmp", 3*time.Hour))
}

func TestScratchOldSessionCandidate(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, "claude-base"), time.Hour)
	sess := mkdir(t, filepath.Join(base, "-work-proj", id1), 3*day)
	cands, kept := scan(t, "agent-scratch", scratchEnv(home, base))
	wantPaths(t, cands, sess)
	if len(kept) != 0 {
		t.Fatalf("kept = %v", kept)
	}
	c := cands[0]
	if c.Category != "agent-scratch" || c.Removal != core.RenameDelete || c.Base != filepath.Join(base, "-work-proj") ||
		c.RuleKey != "agent/claude-scratch@1" {
		t.Fatalf("candidate = %+v", c)
	}
	if err := category(t, "agent-scratch").Proof(scratchEnv(home, base), c); err != nil {
		t.Fatalf("proof: %v", err)
	}
}

func TestScratchActiveTranscriptKeeps(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, "claude-base"), time.Hour)
	sess := mkdir(t, filepath.Join(base, "-work-proj", id1), 3*day)
	other := mkdir(t, filepath.Join(base, "-work-proj", id2), 3*day)
	write(t, filepath.Join(home, ".claude", "projects", "p1", id1+".jsonl"), "{}", time.Hour)
	write(t, filepath.Join(home, ".claude", "projects", "p1", id2+".jsonl"), "{}", 5*day)
	e := scratchEnv(home, base)
	cands, kept := scan(t, "agent-scratch", e)
	wantPaths(t, cands, other)
	if len(kept) != 1 || kept[0].Path != sess || kept[0].Why != "active" {
		t.Fatalf("kept = %v", kept)
	}
	// The transcript becomes active after the scan: Proof keeps it.
	write(t, filepath.Join(home, ".claude", "projects", "p1", id2+".jsonl"), "{}", time.Minute)
	err := category(t, "agent-scratch").Proof(e, cands[0])
	if ke, ok := err.(*core.KeepError); !ok || ke.Why != "active" {
		t.Fatalf("proof = %v, want keep active", err)
	}
}

func TestScratchSiblingInternalsUntouched(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, "claude-base"), time.Hour)
	mkdir(t, filepath.Join(base, "shell-snapshots", "abc"), 5*day)
	write(t, filepath.Join(base, "loose.txt"), "x", 5*day)
	mkdir(t, filepath.Join(base, "-proj", "deep", "er"), 5*day) // depth 3 is not a candidate itself
	sess := mkdir(t, filepath.Join(base, "-proj", id1), 5*day)
	e := scratchEnv(home, base)
	cands, kept := scan(t, "agent-scratch", e)
	wantPaths(t, cands, sess)
	deep := filepath.Join(base, "-proj", "deep")
	if len(kept) != 1 || kept[0].Path != deep || kept[0].Why != "no-marker" {
		t.Fatalf("kept = %v, want %s kept as no-marker", kept, deep)
	}
	notID := core.Candidate{Category: "agent-scratch", RuleKey: "agent/claude-scratch@1", Path: deep,
		Base: filepath.Join(base, "-proj"), Removal: core.RenameDelete}
	if err := category(t, "agent-scratch").Proof(e, notID); err == nil {
		t.Fatal("proof accepted a name that is not a session id")
	}
	bad := core.Candidate{Category: "agent-scratch", RuleKey: "agent/claude-scratch@1",
		Path: filepath.Join(base, "shell-snapshots", "abc"), Base: filepath.Join(base, "shell-snapshots"), Removal: core.RenameDelete}
	if err := category(t, "agent-scratch").Proof(e, bad); err == nil {
		t.Fatal("proof accepted a sibling that does not start with -")
	}
}

func TestScratchFreshMtimeWithoutTranscriptKept(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, "claude-base"), time.Hour)
	young := mkdir(t, filepath.Join(base, "-proj", id1), time.Hour)
	old := mkdir(t, filepath.Join(base, "-proj", id2), 3*day)
	cands, kept := scan(t, "agent-scratch", scratchEnv(home, base))
	wantPaths(t, cands, old)
	if len(kept) != 1 || kept[0].Path != young || kept[0].Why != "young" {
		t.Fatalf("kept = %v", kept)
	}
}

func TestScratchSymlinkedSlugTranscriptKeeps(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, "claude-base"), time.Hour)
	sess := mkdir(t, filepath.Join(base, "-proj", id1), 3*day)
	real := filepath.Join(home, "elsewhere", "p1")
	write(t, filepath.Join(real, id1+".jsonl"), "{}", time.Hour)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, ".claude", "projects", "p1")); err != nil {
		t.Fatal(err)
	}
	cands, kept := scan(t, "agent-scratch", scratchEnv(home, base))
	if len(cands) != 0 || len(kept) != 1 || kept[0].Path != sess || kept[0].Why != "active" {
		t.Fatalf("cands = %v kept = %v", cands, kept)
	}
}

func TestScratchUnreadableProjectsKeepsAll(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, "claude-base"), time.Hour)
	mkdir(t, filepath.Join(base, "-proj", id1), 3*day)
	write(t, filepath.Join(home, ".claude", "projects"), "not a directory", time.Hour) // ReadDir fails: ENOTDIR
	cands, kept := scan(t, "agent-scratch", scratchEnv(home, base))
	if len(cands) != 0 || len(kept) != 1 || kept[0].Why != "active" {
		t.Fatalf("cands = %v kept = %v", cands, kept)
	}
}

func TestScratchHonoursClaudeConfigDir(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, "claude-base"), time.Hour)
	mkdir(t, filepath.Join(base, "-proj", id1), 3*day)
	cfg := filepath.Join(home, "cfg")
	write(t, filepath.Join(cfg, "projects", "p1", id1+".jsonl"), "{}", time.Hour)
	e := scratchEnv(home, base)
	c := category(t, "agent-scratch") // clears the variable; set it after
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	cands, kept, err := c.Scan(e)
	if err != nil || len(cands) != 0 || len(kept) != 1 || kept[0].Why != "active" {
		t.Fatalf("cands = %v kept = %v err = %v", cands, kept, err)
	}
}

func TestCodexArg0Kept(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, ".codex", "tmp"), time.Hour)
	mkdir(t, filepath.Join(base, "arg0"), 5*day)
	run := mkdir(t, filepath.Join(base, "run123"), 5*day)
	e := scratchEnv(home, base)
	cands, _ := scan(t, "agent-scratch", e)
	wantPaths(t, cands, run)
	if cands[0].Base != base || cands[0].RuleKey != "agent/codex-tmp@1" {
		t.Fatalf("candidate = %+v", cands[0])
	}
	arg0 := core.Candidate{Category: "agent-scratch", RuleKey: "agent/codex-tmp@1", Path: filepath.Join(base, "arg0"), Base: base, Removal: core.RenameDelete}
	if err := category(t, "agent-scratch").Proof(e, arg0); err == nil {
		t.Fatal("proof accepted arg0")
	}
}

func transcriptsEnv(home string) *core.Env {
	return newEnv(home, &testutil.FakePlatform{},
		pathRule("agent-transcripts", core.Optional, "claude-transcripts", 90*day),
		pathRule("agent-transcripts", core.Optional, "codex-sessions", 90*day))
}

func TestTranscriptsReportOnlyForClaude(t *testing.T) {
	home := testutil.TempHome(t)
	projects := filepath.Join(home, ".claude", "projects")
	write(t, filepath.Join(projects, "p", "a.jsonl"), strings.Repeat("x", 100), 200*day)
	write(t, filepath.Join(projects, "p", "memory", "MEMORY.md"), "memory", 200*day)
	e := transcriptsEnv(home)
	cands, _ := scan(t, "agent-transcripts", e)
	if len(cands) != 1 || cands[0].Path != projects || cands[0].Removal != core.ReportOnly {
		t.Fatalf("candidates = %+v", cands)
	}
	if !strings.Contains(cands[0].Reason, "30 (default)") || strings.Contains(cands[0].Reason, "lower") {
		t.Fatalf("reason = %q", cands[0].Reason)
	}

	write(t, filepath.Join(home, ".claude", "settings.json"), `{"cleanupPeriodDays": 14}`, time.Hour)
	cands, _ = scan(t, "agent-transcripts", e)
	if len(cands) != 1 || !strings.Contains(cands[0].Reason, "14") {
		t.Fatalf("reason = %+v", cands)
	}

	old := largeTranscripts
	largeTranscripts = 50
	defer func() { largeTranscripts = old }()
	cands, _ = scan(t, "agent-transcripts", e)
	if len(cands) != 1 || !strings.Contains(cands[0].Reason, "lower") {
		t.Fatalf("large reason = %+v", cands)
	}
	if err := category(t, "agent-transcripts").Proof(e, cands[0]); err == nil {
		t.Fatal("proof of a report-only candidate must keep it")
	}
}

func TestTranscriptYoungNeverCandidate(t *testing.T) {
	home := testutil.TempHome(t)
	sessions := filepath.Join(home, ".codex", "sessions")
	write(t, filepath.Join(sessions, "2026", "01", "young.jsonl"), "{}", time.Hour)
	write(t, filepath.Join(sessions, "2026", "01", "mid.jsonl"), "{}", 30*day)
	old := write(t, filepath.Join(sessions, "2025", "01", "old.jsonl"), "{}", 100*day)
	write(t, filepath.Join(sessions, "2025", "01", "notes.txt"), "x", 100*day)
	write(t, filepath.Join(sessions, "2025", "01", "old.jsonl.tmp"), "x", 100*day)
	e := transcriptsEnv(home)
	cands, _ := scan(t, "agent-transcripts", e)
	var codex []core.Candidate
	for _, c := range cands {
		if c.Removal != core.ReportOnly {
			codex = append(codex, c)
		}
	}
	wantPaths(t, codex, old)
	if codex[0].Base != filepath.Dir(old) || codex[0].Removal != core.RenameDelete {
		t.Fatalf("candidate = %+v", codex[0])
	}
	cat := category(t, "agent-transcripts")
	if err := cat.Proof(e, codex[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
	testutil.Touch(t, old, time.Hour)
	if err := cat.Proof(e, codex[0]); err == nil {
		t.Fatal("proof accepted a session modified an hour ago")
	}
}

func TestCleanupPeriodReading(t *testing.T) {
	home := testutil.TempHome(t)
	projects := filepath.Join(home, ".claude", "projects")
	write(t, filepath.Join(projects, "p", "a.jsonl"), "x", time.Hour)
	e := transcriptsEnv(home)
	for settings, want := range map[string]string{
		`{"cleanupPeriodDays": 14.0}`:   "(now 14)",
		`{"cleanupPeriodDays": "21"}`:   "(now 21)",
		`{"cleanupPeriodDays": 14.5}`:   "(now invalid)",
		`{"cleanupPeriodDays": "soon"}`: "(now invalid)",
		`{"other": true}`:               "(now 30 (default))",
		`{not json`:                     "(now unreadable)",
	} {
		write(t, filepath.Join(home, ".claude", "settings.json"), settings, time.Hour)
		cands, _ := scan(t, "agent-transcripts", e)
		if len(cands) != 1 || !strings.Contains(cands[0].Reason, want) {
			t.Fatalf("settings %s: reason %+v, want %q", settings, cands, want)
		}
	}
}

func TestTranscriptsMissingFoldersNothing(t *testing.T) {
	home := testutil.TempHome(t)
	cands, kept := scan(t, "agent-transcripts", transcriptsEnv(home))
	if len(cands) != 0 || len(kept) != 0 {
		t.Fatalf("got %v %v", cands, kept)
	}
}

func versionsEnv(home string) *core.Env {
	return newEnv(home, &testutil.FakePlatform{},
		pathRule("agent-versions", core.Optional, "claude-versions", day),
		pathRule("agent-versions", core.Optional, "claude-plugin-cache", day))
}

func TestVersionsKeepActiveAndPrevious(t *testing.T) {
	home := testutil.TempHome(t)
	dir := filepath.Join(home, ".local", "share", "claude", "versions")
	v1 := write(t, filepath.Join(dir, "1.0.0"), "a", 30*day)
	v2 := write(t, filepath.Join(dir, "1.1.0"), "b", 20*day)
	v3 := write(t, filepath.Join(dir, "1.2.0"), "c", 10*day)
	v0 := write(t, filepath.Join(dir, "0.9.0"), "d", time.Hour) // newest mtime, lowest version: mtime is not the order
	bin := filepath.Join(home, ".local", "bin")
	mkdir(t, bin, time.Hour)
	if err := os.Symlink(v2, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	e := versionsEnv(home)
	cands, _ := scan(t, "agent-versions", e)
	wantPaths(t, cands, v1, v0)
	if cands[0].Base != dir || cands[0].Removal != core.RenameDelete {
		t.Fatalf("candidate = %+v", cands[0])
	}
	cat := category(t, "agent-versions")
	if err := cat.Proof(e, cands[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
	// The active version moves to the oldest one: Proof keeps what became active.
	if err := os.Remove(filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(v0, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := cat.Proof(e, core.Candidate{Category: "agent-versions", RuleKey: "agent/claude-versions@1", Path: v0, Base: dir, Removal: core.RenameDelete}); err == nil {
		t.Fatal("proof accepted the active version")
	}
	_ = v3
}

func TestSemverOrder(t *testing.T) {
	less := func(a, b string) bool {
		x, ok1 := parseVersion(a)
		y, ok2 := parseVersion(b)
		if !ok1 || !ok2 {
			t.Fatalf("%s or %s does not parse", a, b)
		}
		return x.less(y) && !y.less(x)
	}
	for _, p := range [][2]string{{"0.9.0", "0.10.0"}, {"1.2.0", "1.10.0"}, {"1.2.0-beta", "1.2.0"}, {"v1.2.0", "1.2.1"}, {"1.2.0-alpha", "1.2.0-beta"}} {
		if !less(p[0], p[1]) {
			t.Errorf("%s should sort below %s", p[0], p[1])
		}
	}
	for _, bad := range []string{"a1b2c3d", "1.2", "1.2.0.4", "1.2.0.tmp", "1.2.0-", ".tmp", "1.2.x", "1.2.0-b/c", ""} {
		if _, ok := parseVersion(bad); ok {
			t.Errorf("%q must not be a version", bad)
		}
	}
	if got := highest([]string{"/d/0.9.0", "/d/0.10.0", "/d/a1b2c3d"}); got != "/d/0.10.0" {
		t.Errorf("highest = %s", got)
	}
}

func TestVersionsNonVersionNamesNeverOffered(t *testing.T) {
	home := testutil.TempHome(t)
	dir := filepath.Join(home, ".local", "share", "claude", "versions")
	v1 := write(t, filepath.Join(dir, "1.0.0"), "a", 30*day)
	write(t, filepath.Join(dir, "1.1.0"), "b", 20*day)
	v3 := write(t, filepath.Join(dir, "1.2.0"), "c", 10*day)
	write(t, filepath.Join(dir, "a1b2c3d"), "h", 90*day)
	write(t, filepath.Join(dir, "1.3.0.partial"), "p", 90*day)
	write(t, filepath.Join(dir, ".tmp"), "t", 90*day)
	write(t, filepath.Join(dir, "lock"), "l", 90*day)
	bin := filepath.Join(home, ".local", "bin")
	mkdir(t, bin, time.Hour)
	if err := os.Symlink(v3, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	cands, _ := scan(t, "agent-versions", versionsEnv(home))
	wantPaths(t, cands, v1) // 1.1.0 is the highest other one
}

func TestVersionsKeepBothActive(t *testing.T) {
	home := testutil.TempHome(t)
	dir := filepath.Join(home, ".local", "share", "claude", "versions")
	write(t, filepath.Join(dir, "1.0.0"), "a", 30*day)
	v2 := write(t, filepath.Join(dir, "1.1.0"), "b", 20*day)
	v3 := write(t, filepath.Join(dir, "1.2.0"), "c", 10*day)
	v4 := write(t, filepath.Join(dir, "0.9.0"), "d", 40*day)
	bin := filepath.Join(home, ".local", "bin")
	mkdir(t, bin, time.Hour)
	if err := os.Symlink(v2, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "elsewhere", "claude")
	mkdir(t, filepath.Dir(other), time.Hour)
	if err := os.Symlink(v3, other); err != nil {
		t.Fatal(err)
	}
	e := versionsEnv(home)
	e.Run = &testutil.FakeRunner{Paths: map[string]string{"claude": other}}
	cands, _ := scan(t, "agent-versions", e)
	wantPaths(t, cands, v4) // 1.1.0 and 1.2.0 active, 1.0.0 the highest other
}

func TestVersionsUnknownActiveKeepsAll(t *testing.T) {
	home := testutil.TempHome(t)
	dir := filepath.Join(home, ".local", "share", "claude", "versions")
	write(t, filepath.Join(dir, "1.0.0"), "a", 30*day)
	write(t, filepath.Join(dir, "1.1.0"), "b", 20*day)
	write(t, filepath.Join(dir, "1.2.0"), "c", 10*day)
	cands, _ := scan(t, "agent-versions", versionsEnv(home))
	if len(cands) != 0 {
		t.Fatalf("candidates without a known active version: %v", paths(cands))
	}
}

func pluginCache(t *testing.T, home string) (cache string, vs [3]string) {
	t.Helper()
	cache = filepath.Join(home, ".claude", "plugins", "cache")
	vs[0] = mkdir(t, filepath.Join(cache, "mkt", "plug", "1.0.0"), 30*day)
	vs[1] = mkdir(t, filepath.Join(cache, "mkt", "plug", "1.1.0"), time.Hour) // newest mtime, middle version
	vs[2] = mkdir(t, filepath.Join(cache, "mkt", "plug", "1.2.0"), 10*day)
	return cache, vs
}

func installedJSON(t *testing.T, home, entries string) {
	t.Helper()
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"plug@mkt":[`+entries+`]}}`, time.Hour)
}

func TestPluginCacheNonVersionNamesNeverOffered(t *testing.T) {
	home := testutil.TempHome(t)
	cache := filepath.Join(home, ".claude", "plugins", "cache", "mkt", "plug")
	mkdir(t, filepath.Join(cache, "a1b2c3d"), 90*day)
	mkdir(t, filepath.Join(cache, "1.2.0"), 30*day)
	inst := mkdir(t, filepath.Join(cache, "1.10.0"), 90*day)
	installedJSON(t, home, `{"installPath":"`+inst+`","version":"1.10.0"}`)
	cands, _ := scan(t, "agent-versions", versionsEnv(home))
	if len(cands) != 0 {
		t.Fatalf("offered %v: 1.2.0 is the highest other, a1b2c3d is no version", paths(cands))
	}
	old := mkdir(t, filepath.Join(cache, "1.1.0"), 90*day)
	cands, _ = scan(t, "agent-versions", versionsEnv(home))
	wantPaths(t, cands, old)
}

func TestPluginCacheKeepsDirNamedLikeInstalledVersion(t *testing.T) {
	home := testutil.TempHome(t)
	_, vs := pluginCache(t, home)
	// The installPath is stale, but the entry's version names the 1.0.0 directory.
	installedJSON(t, home, `{"installPath":"`+filepath.Join(home, "gone")+`","version":"1.0.0"}`)
	cands, _ := scan(t, "agent-versions", versionsEnv(home))
	wantPaths(t, cands, vs[1])
}

func TestPluginCacheUnmatchedInstalledEntryKeepsPlugin(t *testing.T) {
	home := testutil.TempHome(t)
	pluginCache(t, home)
	// A second plugin proves the others are still cleaned.
	other := filepath.Join(home, ".claude", "plugins", "cache", "mkt", "plug2")
	o2 := mkdir(t, filepath.Join(other, "1.0.0"), 30*day)
	mkdir(t, filepath.Join(other, "1.1.0"), 20*day)
	mkdir(t, filepath.Join(other, "1.2.0"), 10*day)
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"plugins":{"plug@mkt":[{"installPath":"`+filepath.Join(home, "gone")+`","version":"9.9.9"}],`+
			`"plug2@mkt":[{"installPath":"`+filepath.Join(other, "1.2.0")+`","version":"1.2.0"}]}}`, time.Hour)
	cands, _ := scan(t, "agent-versions", versionsEnv(home))
	wantPaths(t, cands, o2)
}

func TestPluginCacheKeepsInstalledAndPrevious(t *testing.T) {
	home := testutil.TempHome(t)
	cache, vs := pluginCache(t, home)
	// An older plugin version on its own: installed, so kept; a second one is the only other.
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"plug@mkt":[{"scope":"user","installPath":"`+vs[0]+`","version":"1.0.0"}]}}`, time.Hour)
	e := versionsEnv(home)
	cands, _ := scan(t, "agent-versions", e)
	wantPaths(t, cands, vs[1])
	if cands[0].Base != filepath.Join(cache, "mkt", "plug") || cands[0].RuleKey != "agent/claude-plugin-cache@1" {
		t.Fatalf("candidate = %+v", cands[0])
	}
	cat := category(t, "agent-versions")
	if err := cat.Proof(e, cands[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
	if err := cat.Proof(e, core.Candidate{Category: "agent-versions", RuleKey: "agent/claude-plugin-cache@1", Path: vs[0],
		Base: cands[0].Base, Removal: core.RenameDelete}); err == nil {
		t.Fatal("proof accepted the installed version")
	}
}

func TestPluginCacheUnreadableKeepsAll(t *testing.T) {
	home := testutil.TempHome(t)
	pluginCache(t, home)
	e := versionsEnv(home)
	if cands, _ := scan(t, "agent-versions", e); len(cands) != 0 {
		t.Fatalf("missing installed_plugins.json: %v", paths(cands))
	}
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{not json`, time.Hour)
	if cands, _ := scan(t, "agent-versions", e); len(cands) != 0 {
		t.Fatalf("corrupt installed_plugins.json: %v", paths(cands))
	}
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{"version":2,"plugins":{}}`, time.Hour)
	if cands, _ := scan(t, "agent-versions", e); len(cands) != 0 {
		t.Fatalf("empty installed_plugins.json: %v", paths(cands))
	}
}
