package install

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/als0x/sapulidi/internal/testutil"
)

const pluginJSON = `{"name":"sapulidi","version":"0.1.0"}`

func plugFS() fs.FS {
	return fstest.MapFS{
		".claude-plugin/plugin.json":             {Data: []byte(pluginJSON)},
		"hooks/claude-session-start.sh":          {Data: []byte("#!/bin/sh\n")},
		"bin/sapulidi":                           {Data: []byte("#!/bin/sh\n")},
		"README.md":                              {Data: []byte("readme\n")},
		"skills/sapulidi/SKILL.md":               {Data: []byte("skill\n")},
		"skills/sapulidi/references/codex.md":    {Data: []byte("codex\n")},
		"skills/sapulidi/references/pi.md":       {Data: []byte("pi\n")},
		"skills/sapulidi/references/cursor.md":   {Data: []byte("cursor\n")},
		"skills/sapulidi/references/opencode.md": {Data: []byte("opencode\n")},
	}
}

func newEnv(t *testing.T) (*Env, string) {
	t.Helper()
	home := testutil.TempHome(t)
	return &Env{Home: home, Data: filepath.Join(home, ".local", "share", "sapulidi"), Plugin: plugFS()}, home
}

// fakeClaude puts a recording `claude` on PATH; list is what `plugin list --json` prints. It returns the log path.
func fakeClaude(t *testing.T, list string) string {
	t.Helper()
	dir := t.TempDir()
	state := t.TempDir()
	logPath := filepath.Join(state, "log")
	for name, body := range map[string]string{"list.json": list, "mlist.json": "[]", "fail": ""} {
		if err := os.WriteFile(filepath.Join(state, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// "fail" holds argv prefixes that exit 1; list.json and mlist.json answer the two list commands.
	script := "#!/bin/sh\nd='" + state + "'\necho \"$@\" >> \"$d/log\"\n" +
		"while read -r p; do [ -n \"$p\" ] && case \"$*\" in \"$p\"*) echo boom >&2; exit 1;; esac; done < \"$d/fail\"\n" +
		"[ \"$*\" = 'plugin list --json' ] && cat \"$d/list.json\"\n" +
		"[ \"$*\" = 'plugin marketplace list --json' ] && cat \"$d/mlist.json\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return logPath
}

// setFake rewrites one state file of the fake claude next to logp.
func setFake(t *testing.T, logp, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(logp), name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func noTools(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")
}

func readLog(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func eq(a, b []string) bool { return strings.Join(a, "\n") == strings.Join(b, "\n") }

func TestClaudeInstallRunsExpectedCommands(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[]`)
	r, err := e.Install("claude")
	if err != nil || r[0].Status != "installed" {
		t.Fatalf("got %+v %v", r, err)
	}
	plug := filepath.Join(e.Data, "plugin")
	want := []string{"plugin list --json", "plugin marketplace list --json", "plugin marketplace add " + plug, "plugin install sapulidi@als0x-sapulidi"}
	if got := readLog(t, logp); !eq(got, want) {
		t.Fatalf("commands %q want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(plug, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	rec, err := e.load()
	if err != nil || len(rec.Entries) != 1 || rec.Entries[0].Host != "claude" || len(rec.Entries[0].Files) != 9 {
		t.Fatalf("record %+v %v", rec, err)
	}
	for _, f := range rec.Entries[0].Files {
		if len(f.SHA256) != 64 {
			t.Fatalf("no hash for %s", f.Path)
		}
	}
}

func TestClaudeAlreadyInstalledRunsNothing(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[{"id":"sapulidi@als0x-sapulidi","version":"0.1.0","scope":"user"}]`)
	r, _ := e.Install("claude")
	if r[0].Status != "already installed" {
		t.Fatalf("got %+v", r)
	}
	if got := readLog(t, logp); !eq(got, []string{"plugin list --json"}) {
		t.Fatalf("commands %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.Data, "plugin")); err == nil {
		t.Fatal("extracted although already installed")
	}
}

func TestClaudeOtherSourceRefuses(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[{"id":"sapulidi@someone-else","version":"9.9.9"}]`)
	r, _ := e.Install("claude")
	if r[0].Status != "refused" || !strings.Contains(r[0].Detail, "claude plugin uninstall sapulidi@someone-else") {
		t.Fatalf("got %+v", r)
	}
	if got := readLog(t, logp); !eq(got, []string{"plugin list --json"}) {
		t.Fatalf("commands %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.Data, "plugin")); err == nil {
		t.Fatal("extracted although refused")
	}
}

func TestClaudeUpgradeReinstallsWhenVersionsDiffer(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[]`)
	if r, _ := e.Install("claude"); r[0].Status != "installed" {
		t.Fatalf("got %+v", r)
	}
	// sapulidi installed it, then the installed version falls behind the embedded one
	setFake(t, logp, "list.json", `[{"id":"sapulidi@als0x-sapulidi","version":"0.0.9"}]`)
	setFake(t, logp, "log", "")
	r, _ := e.Install("claude")
	if r[0].Status != "upgraded" {
		t.Fatalf("got %+v", r)
	}
	want := []string{"plugin list --json", "plugin install sapulidi@als0x-sapulidi"}
	if got := readLog(t, logp); !eq(got, want) {
		t.Fatalf("commands %q want %q", got, want)
	}
	// a second upgrade replaces the extracted folder
	if _, err := e.Install("claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Data, "plugin", "README.md")); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeMissingPrintsTheTwoCommands(t *testing.T) {
	e, _ := newEnv(t)
	noTools(t)
	r, err := e.Install("claude")
	if err != nil || r[0].Status != "not run" {
		t.Fatalf("got %+v %v", r, err)
	}
	plug := filepath.Join(e.Data, "plugin")
	for _, want := range []string{"claude plugin marketplace add " + plug, "claude plugin install sapulidi@als0x-sapulidi"} {
		if !strings.Contains(r[0].Detail, want) {
			t.Fatalf("detail %q lacks %q", r[0].Detail, want)
		}
	}
}

func TestOtherHostCopiesOnlyWhenFolderExists(t *testing.T) {
	e, home := newEnv(t)
	noTools(t)
	r, _ := e.Install("codex")
	if r[0].Status != "skipped" || !strings.Contains(r[0].Detail, "does not exist") {
		t.Fatalf("got %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
		t.Fatal("created the tool's config folder")
	}
	skills := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Install("codex")
	if r[0].Status != "installed" || r[0].Tested {
		t.Fatalf("got %+v", r)
	}
	if _, err := os.Stat(filepath.Join(skills, "sapulidi", "references", "codex.md")); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(skills, "sapulidi", "mine.md")
	other := filepath.Join(skills, "other-skill.md")
	for _, p := range []string{mine, other} {
		if err := os.WriteFile(p, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rr, err := e.Remove("codex")
	if err != nil || rr[0].Status != "removed" || !strings.Contains(strings.Join(rr[0].Notes, "\n"), "not empty, kept") {
		t.Fatalf("got %+v %v", rr, err)
	}
	for _, p := range []string{mine, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("user file %s removed", p)
		}
	}
	if _, err := os.Stat(filepath.Join(skills, "sapulidi", "SKILL.md")); err == nil {
		t.Fatal("recorded file survived")
	}
}

func TestRemoveCleansEverythingItInstalled(t *testing.T) {
	e, home := newEnv(t)
	logp := fakeClaude(t, `[]`)
	skills := filepath.Join(home, ".pi", "agent", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Install("pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Install("claude"); err != nil {
		t.Fatal(err)
	}
	// claude now lists the plugin as installed
	listPath := filepath.Join(filepath.Dir(logp), "list.json")
	if err := os.WriteFile(listPath, []byte(`[{"id":"sapulidi@als0x-sapulidi","version":"0.1.0"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(logp); err != nil {
		t.Fatal(err)
	}
	rr, err := e.Remove("all")
	if err != nil || len(rr) != 2 {
		t.Fatalf("got %+v %v", rr, err)
	}
	want := []string{"plugin list --json", "plugin uninstall sapulidi@als0x-sapulidi", "plugin marketplace remove als0x-sapulidi"}
	if got := readLog(t, logp); !eq(got, want) {
		t.Fatalf("commands %q want %q", got, want)
	}
	for _, p := range []string{filepath.Join(e.Data, "plugin"), filepath.Join(skills, "sapulidi")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s still there", p)
		}
	}
	if _, err := os.Stat(skills); err != nil {
		t.Fatal("removed the tool's skills folder")
	}
	rec, _ := e.load()
	if len(rec.Entries) != 0 {
		t.Fatalf("record not cleared: %+v", rec)
	}
}

func TestDetectFromTempHome(t *testing.T) {
	e, home := newEnv(t)
	noTools(t)
	for _, d := range []string{".codex", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var present, tested []string
	for _, d := range e.Detect() {
		if d.Present {
			present = append(present, d.Host.Name)
		}
		if d.Host.Tested {
			tested = append(tested, d.Host.Name)
		}
	}
	sort.Strings(present)
	if !eq(present, []string{"codex", "cursor"}) || !eq(tested, []string{"claude"}) {
		t.Fatalf("present %v tested %v", present, tested)
	}
	if len(Hosts) != 6 {
		t.Fatalf("hosts table has %d rows", len(Hosts))
	}
}

func TestDetectByCommandOnPath(t *testing.T) {
	e, _ := newEnv(t)
	fakeClaude(t, `[]`)
	for _, d := range e.Detect() {
		if d.Present != (d.Host.Name == "claude") {
			t.Fatalf("%s present=%v", d.Host.Name, d.Present)
		}
	}
}

func TestInstallAllSkipsWithReason(t *testing.T) {
	e, home := newEnv(t)
	noTools(t)
	if err := os.MkdirAll(filepath.Join(home, ".cursor", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range e.InstallAll() {
		got[r.Host] = r.Status
	}
	if got["cursor"] != "installed" || got["codex"] != "skipped" || got["pi"] != "skipped" {
		t.Fatalf("got %v", got)
	}
}

func TestTreeOutsideAllowedPathsUntouched(t *testing.T) {
	e, home := newEnv(t)
	logp := fakeClaude(t, `[]`)
	_ = logp
	skills := filepath.Join(home, ".cursor", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cursor", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, home)
	if _, err := e.Install("claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Install("cursor"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Remove("all"); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, home)
	allowed := []string{e.Data, filepath.Join(skills, "sapulidi")}
	for p, h := range after {
		if before[p] == h {
			continue
		}
		ok := false
		for _, a := range allowed {
			if p == a || strings.HasPrefix(p, a+string(filepath.Separator)) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("changed outside the allowed paths: %s", p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Errorf("vanished: %s", p)
		}
	}
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			m[p] = "dir"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		s := sha256.Sum256(b)
		m[p] = hex.EncodeToString(s[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// ---- removal safety ----

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestRemoveRecordedRefusesRootOutsideAllowed(t *testing.T) {
	e, home := newEnv(t)
	victim := filepath.Join(home, "victim")
	write(t, filepath.Join(victim, "a.txt"), "a")
	_, notes := removeRecorded(e.Data, home, Entry{Host: "claude", Root: victim, Files: []FileRec{{Path: "a.txt", SHA256: sum("a")}}})
	if !exists(filepath.Join(victim, "a.txt")) || len(notes) == 0 {
		t.Fatalf("removed outside the allowed roots: %v", notes)
	}
	// a skills-looking folder that is not under a host skills folder
	odd := filepath.Join(home, "elsewhere", "sapulidi")
	write(t, filepath.Join(odd, "a.txt"), "a")
	removeRecorded(e.Data, home, Entry{Host: "codex", Root: odd, Files: []FileRec{{Path: "a.txt", SHA256: sum("a")}}})
	if !exists(filepath.Join(odd, "a.txt")) {
		t.Fatal("removed under a folder that is no host skills folder")
	}
	// path escaping the root
	plug := filepath.Join(e.Data, "plugin")
	write(t, filepath.Join(plug, "keep"), "k")
	write(t, filepath.Join(e.Data, "agents.json"), "x")
	removeRecorded(e.Data, home, Entry{Host: "claude", Root: plug, Files: []FileRec{{Path: "../agents.json", SHA256: sum("x")}}})
	if !exists(filepath.Join(e.Data, "agents.json")) {
		t.Fatal("followed .. out of the root")
	}
}

func TestRemoveRecordedNeverFollowsSymlinks(t *testing.T) {
	e, home := newEnv(t)
	plug := filepath.Join(e.Data, "plugin")
	outside := filepath.Join(home, "outside")
	write(t, filepath.Join(outside, "x"), "x")
	if err := os.MkdirAll(plug, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x"), filepath.Join(plug, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(plug, "dir")); err != nil {
		t.Fatal(err)
	}
	_, notes := removeRecorded(e.Data, home, Entry{Host: "claude", Root: plug, Files: []FileRec{
		{Path: "link", SHA256: sum("x")}, {Path: "dir/x", SHA256: sum("x")}}})
	if !exists(filepath.Join(outside, "x")) || !exists(filepath.Join(plug, "link")) || len(notes) < 2 {
		t.Fatalf("followed a symlink: %v", notes)
	}
	// root itself a symlink
	real := filepath.Join(home, "realplug")
	write(t, filepath.Join(real, "a"), "a")
	e2 := filepath.Join(home, "d2")
	if err := os.MkdirAll(e2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(e2, "plugin")); err != nil {
		t.Fatal(err)
	}
	removeRecorded(e2, home, Entry{Host: "claude", Root: filepath.Join(e2, "plugin"), Files: []FileRec{{Path: "a", SHA256: sum("a")}}})
	if !exists(filepath.Join(real, "a")) {
		t.Fatal("removed through a symlinked root")
	}
}

func TestRemoveRecordedKeepsEditedFile(t *testing.T) {
	e, home := newEnv(t)
	plug := filepath.Join(e.Data, "plugin")
	write(t, filepath.Join(plug, "a"), "edited")
	write(t, filepath.Join(plug, "b"), "b")
	n, notes := removeRecorded(e.Data, home, Entry{Host: "claude", Root: plug, Files: []FileRec{{Path: "a", SHA256: sum("original")}, {Path: "b", SHA256: sum("b")}}})
	if n != 1 || !exists(filepath.Join(plug, "a")) || exists(filepath.Join(plug, "b")) {
		t.Fatalf("removed %d, notes %v", n, notes)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "changed since install, kept") {
		t.Fatalf("not reported: %v", notes)
	}
}

func TestRemoveRecordedKeepsFolderWithUnrecordedFile(t *testing.T) {
	e, home := newEnv(t)
	plug := filepath.Join(e.Data, "plugin")
	write(t, filepath.Join(plug, "sub", "a"), "a")
	write(t, filepath.Join(plug, "sub", "stranger"), "s")
	removeRecorded(e.Data, home, Entry{Host: "claude", Root: plug, Files: []FileRec{{Path: "sub/a", SHA256: sum("a")}}})
	if !exists(filepath.Join(plug, "sub", "stranger")) || exists(filepath.Join(plug, "sub", "a")) {
		t.Fatal("wrong survivors")
	}
}

func TestRemoveRecordedMissingPathIsFine(t *testing.T) {
	e, home := newEnv(t)
	plug := filepath.Join(e.Data, "plugin")
	n, notes := removeRecorded(e.Data, home, Entry{Host: "claude", Root: plug, Files: []FileRec{{Path: "gone", SHA256: sum("g")}}})
	if n != 0 || len(notes) != 0 {
		t.Fatalf("%d %v", n, notes)
	}
	write(t, filepath.Join(plug, "here"), "h")
	n, notes = removeRecorded(e.Data, home, Entry{Host: "claude", Root: plug, Files: []FileRec{{Path: "gone", SHA256: sum("g")}, {Path: "here", SHA256: sum("h")}}})
	if n != 1 || len(notes) != 0 || exists(plug) {
		t.Fatalf("%d %v", n, notes)
	}
}

// Only removeRecorded may delete: no other function in internal/install calls os.Remove, os.RemoveAll or syscall.Unlink.
func TestOnlyRemoveRecordedDeletes(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, bad := range deletions(t, f) {
			t.Errorf("%s: %s", f, bad)
		}
	}
}
