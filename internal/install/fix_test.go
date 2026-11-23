package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ours = `[{"name":"sapulidi","marketplace":"als0x-sapulidi","version":"0.0.1"}]`

func logHas(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func entries(t *testing.T, e *Env) []Entry {
	t.Helper()
	rec, err := e.load()
	if err != nil {
		t.Fatal(err)
	}
	return rec.Entries
}

// 1. A plugin or marketplace the user installed is never taken over.
func TestUserOwnedPluginIsLeftAlone(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, ours)
	r, _ := e.Install("claude")
	if r[0].Status != "already installed" || !strings.Contains(r[0].Detail, "you") {
		t.Fatalf("got %+v", r)
	}
	if len(entries(t, e)) != 0 {
		t.Fatal("recorded a plugin sapulidi did not install")
	}
	if _, err := os.Stat(filepath.Join(e.Data, "plugin")); err == nil {
		t.Fatal("extracted although the user owns it")
	}
	rr, _ := e.Remove("claude")
	if rr[0].Status != "nothing recorded" {
		t.Fatalf("got %+v", rr)
	}
	got := readLog(t, logp)
	if logHas(got, "uninstall") || logHas(got, "marketplace remove") || logHas(got, "marketplace add") || logHas(got, "plugin install") {
		t.Fatalf("touched the user's plugin: %q", got)
	}
}

func TestUserOwnedMarketplaceIsLeftAlone(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[]`)
	setFake(t, logp, "mlist.json", `[{"name":"als0x-sapulidi"}]`)
	r, _ := e.Install("claude")
	if r[0].Status != "already installed" || len(entries(t, e)) != 0 {
		t.Fatalf("got %+v", r)
	}
	if got := readLog(t, logp); logHas(got, "marketplace add") || logHas(got, "plugin install") {
		t.Fatalf("commands %q", got)
	}
}

// 2. A failed remove keeps the record, the folder and the steps still to do.
func TestRemoveKeepsRecordWhenClaudeStepFails(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[]`)
	if r, _ := e.Install("claude"); r[0].Status != "installed" {
		t.Fatalf("got %+v", r)
	}
	setFake(t, logp, "list.json", ours)
	setFake(t, logp, "fail", "plugin uninstall\n")
	rr, _ := e.Remove("claude")
	if rr[0].Status != "partial" || !rr[0].Failed() || len(entries(t, e)) != 1 {
		t.Fatalf("got %+v", rr)
	}
	if _, err := os.Stat(filepath.Join(e.Data, "plugin", "README.md")); err != nil {
		t.Fatal("deleted the folder although a step failed")
	}
	// the uninstall works now, but the marketplace step fails: only that one is left
	setFake(t, logp, "fail", "plugin marketplace remove\n")
	setFake(t, logp, "log", "")
	rr, _ = e.Remove("claude")
	en := entries(t, e)
	if rr[0].Status != "partial" || len(en) != 1 || en[0].InstalledPlugin || !en[0].AddedMarketplace {
		t.Fatalf("got %+v %+v", rr, en)
	}
	setFake(t, logp, "fail", "")
	setFake(t, logp, "log", "")
	rr, _ = e.Remove("claude")
	if rr[0].Status != "removed" || len(entries(t, e)) != 0 {
		t.Fatalf("got %+v", rr)
	}
	if got := readLog(t, logp); !eq(got, []string{"plugin marketplace remove als0x-sapulidi"}) {
		t.Fatalf("rerun repeated finished steps: %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.Data, "plugin")); err == nil {
		t.Fatal("folder left after a complete remove")
	}
}

func TestRemoveKeepsRecordWhenClaudeMissingOrListFails(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[]`)
	if _, err := e.Install("claude"); err != nil {
		t.Fatal(err)
	}
	setFake(t, logp, "fail", "plugin list\n")
	rr, _ := e.Remove("claude")
	if rr[0].Status != "partial" || len(entries(t, e)) != 1 {
		t.Fatalf("list failure: %+v", rr)
	}
	noTools(t)
	rr, _ = e.Remove("claude")
	if rr[0].Status != "partial" || len(entries(t, e)) != 1 {
		t.Fatalf("no claude: %+v", rr)
	}
	if _, err := os.Stat(filepath.Join(e.Data, "plugin", "README.md")); err != nil {
		t.Fatal("deleted the folder")
	}
}

// 3. An edited file stops the upgrade before anything is deleted.
func TestUpgradeWithEditedFileLeavesOldTreeIntact(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[]`)
	if _, err := e.Install("claude"); err != nil {
		t.Fatal(err)
	}
	plug := filepath.Join(e.Data, "plugin")
	edited := filepath.Join(plug, "README.md")
	write(t, edited, "mine")
	write(t, filepath.Join(plug, "stranger"), "s")
	setFake(t, logp, "list.json", `[{"id":"sapulidi@als0x-sapulidi","version":"0.0.9"}]`)
	r, _ := e.Install("claude")
	if r[0].Status != "failed" {
		t.Fatalf("got %+v", r)
	}
	for p, want := range map[string]string{edited: "mine", filepath.Join(plug, "stranger"): "s", filepath.Join(plug, "bin", "sapulidi"): "#!/bin/sh\n"} {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", p, b, err)
		}
	}
	left, _ := filepath.Glob(filepath.Join(e.Data, "plugin-*"))
	if len(left) != 0 {
		t.Fatalf("leftovers %v", left)
	}
}

func TestUpgradeSwapLeavesNoStagingOrOldFolder(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[]`)
	if _, err := e.Install("claude"); err != nil {
		t.Fatal(err)
	}
	setFake(t, logp, "list.json", `[{"id":"sapulidi@als0x-sapulidi","version":"0.0.9"}]`)
	if r, _ := e.Install("claude"); r[0].Status != "upgraded" {
		t.Fatalf("got %+v", r)
	}
	left, _ := filepath.Glob(filepath.Join(e.Data, "plugin-*"))
	if len(left) != 0 {
		t.Fatalf("leftovers %v", left)
	}
	if _, err := os.Stat(filepath.Join(e.Data, "plugin", "bin", "sapulidi")); err != nil {
		t.Fatal(err)
	}
}

// 4. A rerun after "marketplace add ok, plugin install failed" does not add the marketplace again.
func TestRerunSkipsMarketplaceAlreadyAddedByRecord(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[]`)
	setFake(t, logp, "fail", "plugin install\n")
	if r, _ := e.Install("claude"); r[0].Status != "failed" {
		t.Fatalf("got %+v", r)
	}
	if en := entries(t, e); len(en) != 1 || !en[0].AddedMarketplace || en[0].InstalledPlugin {
		t.Fatalf("flags %+v", en)
	}
	// the marketplace now exists, and a second add would fail
	setFake(t, logp, "mlist.json", `[{"name":"als0x-sapulidi"}]`)
	setFake(t, logp, "fail", "plugin marketplace add\n")
	setFake(t, logp, "log", "")
	r, _ := e.Install("claude")
	if r[0].Status != "installed" {
		t.Fatalf("got %+v", r)
	}
	if got := readLog(t, logp); logHas(got, "marketplace add") || !logHas(got, "plugin install") {
		t.Fatalf("commands %q", got)
	}
	if en := entries(t, e); !en[0].AddedMarketplace || !en[0].InstalledPlugin {
		t.Fatalf("flags %+v", en)
	}
}

func TestFailedAddWithMarketplaceStillAbsentFails(t *testing.T) {
	e, _ := newEnv(t)
	logp := fakeClaude(t, `[]`)
	setFake(t, logp, "fail", "plugin marketplace add\n")
	r, _ := e.Install("claude")
	if r[0].Status != "failed" {
		t.Fatalf("an add that fails with the marketplace still absent must fail: %+v", r)
	}
}

// 5. A plugin list that cannot be trusted refuses the install.
func TestParseListIsStrict(t *testing.T) {
	for _, bad := range []string{``, `{}`, `{"error":"x"}`, `"x"`, `[1]`} {
		if _, err := parseList([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, good := range []string{`[]`, `{"plugins":[]}`, `{"plugins":[{"id":"a@b"}]}`} {
		if _, err := parseList([]byte(good)); err != nil {
			t.Errorf("rejected %q: %v", good, err)
		}
	}
}

func TestInstallRefusesOnUntrustworthyList(t *testing.T) {
	for _, bad := range []string{`{}`, `{"error":"x"}`} {
		e, _ := newEnv(t)
		logp := fakeClaude(t, bad)
		r, _ := e.Install("claude")
		if !r[0].Failed() {
			t.Fatalf("%s: got %+v", bad, r)
		}
		if got := readLog(t, logp); !eq(got, []string{"plugin list --json"}) {
			t.Fatalf("commands %q", got)
		}
	}
}

func TestAnyForeignSapulidiRefuses(t *testing.T) {
	e, _ := newEnv(t)
	fakeClaude(t, `[{"id":"sapulidi@als0x-sapulidi","version":"0.1.0"},{"id":"sapulidi@other","version":"1"}]`)
	r, _ := e.Install("claude")
	if r[0].Status != "refused" || !strings.Contains(r[0].Detail, "sapulidi@other") {
		t.Fatalf("got %+v", r)
	}
}

func TestEmptyMarketplaceGuidanceHasNoDanglingAt(t *testing.T) {
	e, _ := newEnv(t)
	fakeClaude(t, `[{"name":"sapulidi","version":"1"}]`)
	r, _ := e.Install("claude")
	if r[0].Status != "refused" || strings.Contains(r[0].Detail, "sapulidi@\n") || strings.HasSuffix(r[0].Detail, "sapulidi@") || strings.Contains(r[0].Detail, "sapulidi@ ") {
		t.Fatalf("got %+v", r)
	}
}

// 6. A skills folder that is a symlink is never written or removed through.
func TestSymlinkedSkillsFolderRefused(t *testing.T) {
	e, home := newEnv(t)
	noTools(t)
	target := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".codex", "skills")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Install("codex")
	if r[0].Status != "refused" {
		t.Fatalf("got %+v", r)
	}
	if ents, _ := os.ReadDir(target); len(ents) != 0 {
		t.Fatalf("wrote through the symlink: %v", ents)
	}
	// remove with a forged record
	write(t, filepath.Join(target, "sapulidi", "a"), "a")
	n, notes := removeRecorded(e.Data, home, Entry{Host: "codex", Root: filepath.Join(link, "sapulidi"), Files: []FileRec{{Path: "a", SHA256: sum("a")}}})
	if n != 0 || !exists(filepath.Join(target, "sapulidi", "a")) || len(notes) == 0 {
		t.Fatalf("removed through the symlink: %d %v", n, notes)
	}
}

// 7. A record's root belongs to its host.
func TestRecordRootIsBoundToHost(t *testing.T) {
	e, home := newEnv(t)
	plug := filepath.Join(e.Data, "plugin")
	write(t, filepath.Join(plug, "a"), "a")
	skills := filepath.Join(home, ".codex", "skills", "sapulidi")
	write(t, filepath.Join(skills, "a"), "a")
	files := []FileRec{{Path: "a", SHA256: sum("a")}}
	for _, en := range []Entry{
		{Host: "codex", Root: plug, Files: files},               // plugin folder claimed by a skills host
		{Host: "claude", Root: skills, Files: files},            // a skill folder claimed by claude
		{Host: "cursor", Root: skills, Files: files},            // another host's folder
		{Host: "nonsense", Root: plug, Files: files},            // unknown host
		{Host: "codex", Root: filepath.Dir(skills), Files: nil}, // not named sapulidi
	} {
		if n, notes := removeRecorded(e.Data, home, en); n != 0 || len(notes) == 0 {
			t.Fatalf("%+v: removed %d, notes %v", en, n, notes)
		}
	}
	if !exists(filepath.Join(plug, "a")) || !exists(filepath.Join(skills, "a")) {
		t.Fatal("removed a file for a mismatched record")
	}
}

func TestEmptyFilesNeverRemovesTheRoot(t *testing.T) {
	e, home := newEnv(t)
	plug := filepath.Join(e.Data, "plugin")
	if err := os.MkdirAll(plug, 0o755); err != nil {
		t.Fatal(err)
	}
	removeRecorded(e.Data, home, Entry{Host: "claude", Root: plug})
	if !exists(plug) {
		t.Fatal("removed the root of an entry with no files")
	}
}

func TestListReportsCorruptRecord(t *testing.T) {
	e, _ := newEnv(t)
	noTools(t)
	write(t, filepath.Join(e.Data, "agents.json"), "{not json")
	rows, err := e.List()
	if err == nil || len(rows) != len(Hosts) {
		t.Fatalf("err %v rows %d", err, len(rows))
	}
	for _, r := range rows {
		if r.Detail == "" {
			t.Fatalf("%s shows no problem", r.Host.Name)
		}
	}
}
