package roots

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

var now = time.Now()

type fixture struct {
	home string
	plat *testutil.FakePlatform
	run  *testutil.FakeRunner
	cfg  *config.Config
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	h, err := filepath.EvalSymlinks(testutil.TempHome(t))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{home: h, plat: &testutil.FakePlatform{}, run: &testutil.FakeRunner{}, cfg: &config.Config{}}
}

func (f *fixture) discover() ([]core.Root, []core.Kept) {
	return Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: f.run, Now: now})
}

func (f *fixture) repo(t *testing.T, rel string) string {
	t.Helper()
	return testutil.MakeRepo(t, filepath.Join(f.home, rel))
}

// transcript writes a jsonl file whose lines are the given raw lines.
func transcript(t *testing.T, path string, age time.Duration, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, path, age)
}

func cwdLine(cwd string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "cwd": cwd})
	return string(b)
}

func claudeFile(f *fixture, name string) string {
	return filepath.Join(f.home, ".claude", "projects", "slug", name+".jsonl")
}

func hasKept(ks []core.Kept, path, why string) bool {
	for _, k := range ks {
		if k.Path == path && k.Why == why && k.Category == "roots" {
			return true
		}
	}
	return false
}

func TestDiscoverConfigRootsFirst(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	plain := filepath.Join(f.home, "code", "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(f.home, "code", "gone")
	f.cfg.Roots = []string{plain, a, missing}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(a))

	rs, ks := f.discover()
	if len(rs) != 2 || rs[0] != (core.Root{Path: a, Source: "config"}) || rs[1] != (core.Root{Path: plain, Source: "config"}) {
		t.Fatalf("roots = %v", rs)
	}
	if !hasKept(ks, missing, "missing") {
		t.Fatalf("kept = %v", ks)
	}
}

func TestDiscoverClaudeCwd(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	sub := filepath.Join(a, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript(t, claudeFile(f, "s1"), time.Hour,
		`{"type":"summary","summary":"x"}`, `{"type":"user","message":{"cwd":"/nested/ignored"}}`, cwdLine(sub))
	rs, _ := f.discover()
	if len(rs) != 1 || rs[0] != (core.Root{Path: a, Source: "claude"}) {
		t.Fatalf("roots = %v", rs)
	}
}

func TestDiscoverRunsHardenedGit(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	marker := filepath.Join(f.home, "fsmonitor-ran")
	script := filepath.Join(f.home, "fsmonitor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", a, "config", "core.fsmonitor", script).CombinedOutput(); err != nil {
		t.Fatalf("git config: %v %s", err, out)
	}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(a))
	rs, _ := f.discover()
	if len(rs) != 1 {
		t.Fatalf("roots = %v", rs)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Discover ran the repository's core.fsmonitor command")
	}
	seen := false
	for _, c := range f.run.Calls {
		if len(c.Argv) > 0 && c.Argv[0] == "git" {
			seen = true
			if !strings.Contains(strings.Join(c.Argv, " "), "-c core.fsmonitor=false -c core.hooksPath=/dev/null -c core.pager=cat") {
				t.Errorf("git call without the hardening: %v", c.Argv)
			}
		}
	}
	if !seen {
		t.Fatal("no git call recorded")
	}
}

func TestDiscoverCodexCwd(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	b, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": a}})
	transcript(t, filepath.Join(f.home, ".codex", "sessions", "2026", "11", "01", "rollout-1.jsonl"), time.Hour, string(b))
	rs, _ := f.discover()
	if len(rs) != 1 || rs[0] != (core.Root{Path: a, Source: "codex"}) {
		t.Fatalf("roots = %v", rs)
	}
}

func TestDiscoverSkipsCodexWhenAbsent(t *testing.T) {
	f := newFixture(t)
	_, ks := f.discover()
	if !hasKept(ks, "(codex) no ~/.codex/sessions, discovery skipped", "no-codex") {
		t.Fatalf("kept = %v", ks)
	}
}

func TestDiscoverMapsLinkedWorktreeToMain(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	wt := filepath.Join(f.home, "code", "a-wt")
	cmd := exec.Command("git", "-C", a, "worktree", "add", "-b", "wt", wt)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(wt))
	rs, _ := f.discover()
	if len(rs) != 1 || rs[0].Path != a {
		t.Fatalf("roots = %v", rs)
	}
}

func TestDiscoverDropsNonGit(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.home, "code", "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(dir))
	rs, ks := f.discover()
	if len(rs) != 0 || !hasKept(ks, dir, "not-git") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestDiscoverDropsHomeAndSlash(t *testing.T) {
	f := newFixture(t)
	f.cfg.Roots = []string{"/", f.home}
	rs, ks := f.discover()
	if len(rs) != 0 || !hasKept(ks, "/", "deny") || !hasKept(ks, f.home, "deny") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestDiscoverDropsOtherDeviceRoot(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	c := f.repo(t, "code/c")
	f.plat.OtherDevice = map[string]bool{a: true, c: true}
	f.cfg.Roots = []string{c}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(a))
	rs, ks := f.discover()
	if len(rs) != 1 || rs[0].Path != c || !hasKept(ks, a, "device") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestDiscoverDropsNotOwned(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	f.plat.NotOwned = map[string]bool{a: true}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(a))
	rs, ks := f.discover()
	if len(rs) != 0 || !hasKept(ks, a, "owner") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestDiscoverNested(t *testing.T) {
	f := newFixture(t)
	outer := f.repo(t, "code/outer")
	inner := f.repo(t, "code/outer/vendor/inner")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(inner))
	f.cfg.Roots = []string{outer}
	rs, ks := f.discover()
	if len(rs) != 1 || rs[0].Path != outer || !hasKept(ks, inner, "nested") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestDiscoverOldTranscriptsIgnored(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	transcript(t, claudeFile(f, "old"), 181*24*time.Hour, cwdLine(a))
	rs, _ := f.discover()
	if len(rs) != 0 {
		t.Fatalf("roots = %v", rs)
	}
}

func TestDiscoverReadLimit(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	big := `{"type":"user","text":"` + strings.Repeat("x", 5<<20) + `"}`
	transcript(t, claudeFile(f, "big"), time.Hour, big, cwdLine(a))
	if cwd, ok := firstCwd(claudeFile(f, "big"), false); ok || cwd != "" {
		t.Fatalf("cwd after a 5 MB line was read: %q", cwd)
	}
	rs, _ := f.discover()
	if len(rs) != 0 {
		t.Fatalf("roots = %v", rs)
	}
}

func TestDiscoverCapsAt500(t *testing.T) {
	f := newFixture(t)
	for i := range 501 {
		d := filepath.Join(f.home, "code", "d"+strconv.Itoa(1000+i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		f.cfg.Roots = append(f.cfg.Roots, d)
	}
	rs, ks := f.discover()
	if len(rs) != 500 {
		t.Fatalf("len = %d", len(rs))
	}
	capped := 0
	for _, k := range ks {
		if k.Why == "cap" {
			capped++
		}
	}
	if capped != 1 {
		t.Fatalf("cap kept = %d", capped)
	}
}

func TestDiscoverCapKeepsConfigFirst(t *testing.T) {
	f := newFixture(t)
	for i := range 3 {
		d := filepath.Join(f.home, "code", "a"+strconv.Itoa(i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		f.cfg.Roots = append(f.cfg.Roots, d)
	}
	z := f.repo(t, "code/zzz")
	f.cfg.Roots = append(f.cfg.Roots, z)
	rs, _ := f.discover()
	if len(rs) != 4 {
		t.Fatalf("roots = %v", rs)
	}
}

func TestDiscoverRejectsRelativeCwd(t *testing.T) {
	f := newFixture(t)
	f.repo(t, "code/a")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine("."))
	transcript(t, claudeFile(f, "s2"), 2*time.Hour, cwdLine("../x"))
	t.Chdir(filepath.Join(f.home, "code", "a"))
	rs, ks := f.discover()
	if len(rs) != 0 || !hasKept(ks, ".", "not-git") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestDiscoverSeparateGitDirKeepsOwnToplevel(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	moved := filepath.Join(f.home, "gitdirs", "a.git")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(a, ".git"), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, ".git"), []byte("gitdir: "+moved+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(a))
	rs, _ := f.discover()
	if len(rs) != 1 || rs[0].Path != a {
		t.Fatalf("roots = %v", rs)
	}
}

func TestDiscoverTempDirs(t *testing.T) {
	f := newFixture(t)
	tmp := filepath.Join(f.home, "tmpbase")
	hist := f.repo(t, "tmpbase/proj")
	cfgInside := f.repo(t, "tmpbase/cfgproj")
	f.cfg.Roots = []string{tmp, cfgInside}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(hist))
	rs, ks := Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: f.run, Now: now, TempDirs: []string{tmp}})
	if len(rs) != 1 || rs[0].Path != cfgInside {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
	if !hasKept(ks, tmp, "temp") || !hasKept(ks, hist, "temp") {
		t.Fatalf("kept = %v", ks)
	}
}

func TestDiscoverReadsOneFilePerFolderOnceFound(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	b := f.repo(t, "code/b")
	transcript(t, claudeFile(f, "new"), time.Hour, cwdLine(a))
	transcript(t, claudeFile(f, "old"), 2*time.Hour, cwdLine(b))
	rs, _ := f.discover()
	if len(rs) != 1 || rs[0].Path != a {
		t.Fatalf("roots = %v", rs)
	}
}

func TestDiscoverSkipsCwdUnderResolvedToplevel(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	sub := filepath.Join(a, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(a))
	transcript(t, filepath.Join(f.home, ".claude", "projects", "slug2", "s2.jsonl"), time.Hour, cwdLine(sub))
	f.discover()
	revParse := 0
	for _, c := range f.run.Calls {
		if len(c.Argv) > 0 && c.Argv[0] == "git" && slices.Contains(c.Argv, "rev-parse") {
			revParse++
		}
	}
	if revParse != 1 {
		t.Fatalf("git rev-parse ran %d times", revParse)
	}
}

func TestDiscoverBudget(t *testing.T) {
	f := newFixture(t)
	a := f.repo(t, "code/a")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(a))
	_, ks := Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: f.run, Now: now, Budget: time.Nanosecond})
	if !hasKept(ks, "", "budget") {
		t.Fatalf("kept = %v", ks)
	}
}

func TestDiscoverConfigRootWinsOverNested(t *testing.T) {
	f := newFixture(t)
	outer := f.repo(t, "code/outer")
	inner := f.repo(t, "code/outer/inner")
	f.cfg.Roots = []string{inner}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(outer))
	rs, ks := f.discover()
	if len(rs) != 2 {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
	// and a discovered root inside a config root is the one dropped
	f2 := newFixture(t)
	o2 := f2.repo(t, "code/o")
	i2 := f2.repo(t, "code/o/i")
	f2.cfg.Roots = []string{o2}
	transcript(t, claudeFile(f2, "s1"), time.Hour, cwdLine(i2))
	rs, ks = f2.discover()
	if len(rs) != 1 || rs[0].Path != o2 || !hasKept(ks, i2, "nested") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}
