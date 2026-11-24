package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi"
	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/install"
	"github.com/als0x/sapulidi/internal/schedule"
	"github.com/als0x/sapulidi/internal/testutil"
)

type fakeTTY struct{ in *strings.Reader }

func (f fakeTTY) Read(b []byte) (int, error)  { return f.in.Read(b) }
func (f fakeTTY) Write(b []byte) (int, error) { return len(b), nil }
func (f fakeTTY) Close() error                { return nil }

// uninstallFixture seeds binaries, config and state under a temp home and points scheduleSetup at them.
func uninstallFixture(t *testing.T) (*fakeBackend, config.Paths) {
	t.Helper()
	home := testutil.TempHome(t)
	p := config.Paths{
		Config: filepath.Join(home, "cfg", "sapulidi", "config.toml"),
		State:  filepath.Join(home, "state", "sapulidi"),
		Data:   filepath.Join(home, "data", "sapulidi"),
	}
	for _, f := range []string{filepath.Join(p.Data, "bin", "0.1.0", "sapulidi"), p.Config,
		filepath.Join(p.State, "log.jsonl")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	be := &fakeBackend{installed: map[string]bool{schedule.LabelRun: true, schedule.LabelAgent: true}}
	old := scheduleSetup
	scheduleSetup = func() (schedule.Backend, config.Paths, error) { return be, p, nil }
	t.Cleanup(func() { scheduleSetup = old })
	return be, p
}

func has(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestUninstallCommandRemovesScheduleAndBinaries(t *testing.T) {
	be, p := uninstallFixture(t)
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if len(be.installed) != 0 || has(filepath.Join(p.Data, "bin")) {
		t.Fatalf("schedule %v, bin exists %v", be.installed, has(filepath.Join(p.Data, "bin")))
	}
	if !has(p.Config) || !has(p.State) {
		t.Fatal("plain uninstall must keep config and state")
	}
	if !strings.Contains(out.String(), "removed") {
		t.Fatalf("prints nothing about what it removed: %q", out.String())
	}
}

func TestUninstallAllNeedsTerminal(t *testing.T) {
	be, p := uninstallFixture(t)
	oldTTY, oldIs := confirm.OpenTTY, confirm.StdioIsTerminal
	t.Cleanup(func() { confirm.OpenTTY, confirm.StdioIsTerminal = oldTTY, oldIs })
	confirm.StdioIsTerminal = func() bool { return false } // an agent shell, a hook, a pipe
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall", "--all"}, &out, &errb); code != 1 {
		t.Fatalf("code %d, %s", code, errb.String())
	}
	if len(be.installed) != 2 || !has(filepath.Join(p.Data, "bin")) || !has(p.Config) || !has(p.State) {
		t.Fatal("nothing may be removed without a terminal confirmation")
	}
	if !strings.Contains(errb.String(), "terminal") {
		t.Fatalf("should say a terminal is needed: %q", errb.String())
	}

	// A wrong retype removes nothing either.
	confirm.StdioIsTerminal = func() bool { return true }
	confirm.OpenTTY = func() (io.ReadWriteCloser, error) { return fakeTTY{strings.NewReader("yes\n")}, nil }
	if code := Main([]string{"uninstall", "--all"}, &out, &errb); code != 1 || !has(p.State) {
		t.Fatalf("wrong retype: code %d", code)
	}

	// Retyping `uninstall` removes everything.
	confirm.OpenTTY = func() (io.ReadWriteCloser, error) { return fakeTTY{strings.NewReader("uninstall\n")}, nil }
	out.Reset()
	if code := Main([]string{"--json", "uninstall", "--all"}, &out, &errb); code != 0 {
		t.Fatalf("code %d, %s", code, errb.String())
	}
	if has(p.Config) || has(p.State) || has(filepath.Join(p.Data, "bin")) || len(be.installed) != 0 {
		t.Fatal("--all left something behind")
	}
	var doc struct {
		Schedule []string `json:"schedule"`
		Removed  []string `json:"removed"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Schedule) != 2 || len(doc.Removed) == 0 {
		t.Fatalf("%v %s", err, out.String())
	}
}

// fakeMarketplaces is what the fake claude prints for `plugin marketplace list --json`.
var fakeMarketplaces = "[]"

// fakeClaudeCLI puts a recording `claude` on PATH. list is what `plugin list --json` prints; a call whose
// arguments start with failPrefix exits 1. It returns the log path.
func fakeClaudeCLI(t *testing.T, list, failPrefix string) string {
	t.Helper()
	dir, state := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "list.json"), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nd='" + state + "'\necho \"$@\" >> \"$d/log\"\n" +
		"case \"$*\" in '" + failPrefix + "'*) [ -n '" + failPrefix + "' ] && { echo boom >&2; exit 1; };; esac\n" +
		"[ \"$*\" = 'plugin list --json' ] && cat \"$d/list.json\"\n" +
		"[ \"$*\" = 'plugin marketplace list --json' ] && echo '" + fakeMarketplaces + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return filepath.Join(state, "log")
}

const ownPlugins = `[{"name":"sapulidi","marketplace":"als0x-sapulidi","version":"0.0.1"},{"name":"mine","marketplace":"theirs","version":"1"}]`

func readLines(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

// agentsInstalled has sapulidi install its Claude Code plugin into the fixture's data folder.
func agentsInstalled(t *testing.T, p config.Paths) {
	t.Helper()
	fakeClaudeCLI(t, "[]", "")
	home, _ := os.UserHomeDir()
	env := &install.Env{Home: home, Data: p.Data, Plugin: sapulidi.Plugin}
	if r, err := env.Install("claude"); err != nil || r[0].Status != "installed" {
		t.Fatalf("install: %+v %v", r, err)
	}
}

func TestUninstallRemovesWhatAgentsInstalled(t *testing.T) {
	be, p := uninstallFixture(t)
	agentsInstalled(t, p)
	logp := fakeClaudeCLI(t, ownPlugins, "")
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	log := readLines(t, logp)
	if !strings.Contains(log, "plugin uninstall sapulidi@als0x-sapulidi") || !strings.Contains(log, "plugin marketplace remove als0x-sapulidi") {
		t.Fatalf("claude saw %q", log)
	}
	if strings.Contains(log, "mine") {
		t.Fatalf("touched a plugin that is not ours: %q", log)
	}
	if has(filepath.Join(p.Data, "plugin")) || has(filepath.Join(p.Data, "bin")) || len(be.installed) != 0 {
		t.Fatal("plugin folder, binaries or schedule left")
	}
}

func TestUninstallJSONIncludesAgents(t *testing.T) {
	_, p := uninstallFixture(t)
	agentsInstalled(t, p)
	fakeClaudeCLI(t, ownPlugins, "")
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "uninstall"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	var doc struct {
		Agents []install.Result `json:"agents"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Agents) != 1 || doc.Agents[0].Status != "removed" {
		t.Fatalf("%v %s", err, out.String())
	}
}

func TestUninstallKeepsDataWhenAgentsStepFails(t *testing.T) {
	be, p := uninstallFixture(t)
	agentsInstalled(t, p)
	logp := fakeClaudeCLI(t, ownPlugins, "plugin uninstall")
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall"}, &out, &errb); code != 1 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if !strings.Contains(readLines(t, logp), "plugin uninstall sapulidi@als0x-sapulidi") {
		t.Fatalf("the failing step never ran: %q", readLines(t, logp))
	}
	if len(be.installed) != 0 {
		t.Fatalf("schedule left: %v", be.installed)
	}
	if !has(filepath.Join(p.Data, "plugin")) || !has(filepath.Join(p.Data, "agents.json")) || !has(filepath.Join(p.Data, "bin", "0.1.0", "sapulidi")) {
		t.Fatal("deleted <data> although an agents step failed")
	}
	if !strings.Contains(errb.String(), "sapulidi agents remove all") {
		t.Fatalf("does not say what to run: %q", errb.String())
	}
}

func TestUninstallRefusesBadDataDirBeforeAnyRemoval(t *testing.T) {
	_, p := uninstallFixture(t)
	home, _ := os.UserHomeDir()
	bad := p
	bad.Data = filepath.Join(home, "notsapulidi")
	agentsInstalled(t, bad)
	scheduleSetup = func() (schedule.Backend, config.Paths, error) {
		return &fakeBackend{installed: map[string]bool{schedule.LabelRun: true}}, bad, nil
	}
	logp := fakeClaudeCLI(t, ownPlugins, "")
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall"}, &out, &errb); code != 1 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if got := readLines(t, logp); got != "" {
		t.Fatalf("claude was called: %q", got)
	}
	if !has(filepath.Join(bad.Data, "agents.json")) || !has(filepath.Join(bad.Data, "plugin", ".claude-plugin", "plugin.json")) {
		t.Fatal("record or plugin folder touched")
	}
}

func TestUninstallKeepsEditedFileAndFinishes(t *testing.T) {
	_, p := uninstallFixture(t)
	agentsInstalled(t, p)
	fakeClaudeCLI(t, ownPlugins, "")
	edited := filepath.Join(p.Data, "plugin", ".claude-plugin", "plugin.json")
	if err := os.WriteFile(edited, []byte("{\"mine\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "uninstall"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	var doc struct {
		Agents []install.Result `json:"agents"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Agents) != 1 || !strings.Contains(strings.Join(doc.Agents[0].Notes, "\n"), "plugin.json: changed since install, kept") {
		t.Fatalf("%v %s", err, out.String())
	}
	if !has(edited) || has(filepath.Join(p.Data, "bin")) {
		t.Fatal("edited file must stay and the binaries must go")
	}
}

func TestUninstallHumanOutputNamesKeptFile(t *testing.T) {
	_, p := uninstallFixture(t)
	agentsInstalled(t, p)
	fakeClaudeCLI(t, ownPlugins, "")
	edited := filepath.Join(p.Data, "plugin", ".claude-plugin", "plugin.json")
	if err := os.WriteFile(edited, []byte("{\"mine\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "kept ") || !strings.Contains(out.String(), "plugin.json") {
		t.Fatalf("code %d %q", code, out.String())
	}
}

func TestUninstallLeavesPluginNotAddedBySapulidi(t *testing.T) {
	_, p := uninstallFixture(t)
	fakeMarketplaces = `[{"name":"als0x-sapulidi"}]`
	t.Cleanup(func() { fakeMarketplaces = "[]" })
	logp := fakeClaudeCLI(t, ownPlugins, "")
	home, _ := os.UserHomeDir()
	env := &install.Env{Home: home, Data: p.Data, Plugin: sapulidi.Plugin}
	if r, err := env.Install("claude"); err != nil || r[0].Status != "already installed" {
		t.Fatalf("install: %+v %v", r, err)
	}
	_ = os.Remove(logp)
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if log := readLines(t, logp); strings.Contains(log, "plugin uninstall") || strings.Contains(log, "marketplace remove") {
		t.Fatalf("removed a plugin sapulidi did not add: %q", log)
	}
}

func TestUninstallKeepsDataOnCorruptRecord(t *testing.T) {
	be, p := uninstallFixture(t)
	rec := filepath.Join(p.Data, "agents.json")
	if err := os.WriteFile(rec, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall"}, &out, &errb); code != 1 {
		t.Fatalf("code %d", code)
	}
	msg := errb.String()
	if !strings.Contains(msg, rec) || !strings.Contains(msg, "by hand") || strings.Contains(msg, "agents remove all") {
		t.Fatalf("wrong hint: %q", msg)
	}
	if len(be.installed) != 0 || !has(rec) || !has(filepath.Join(p.Data, "bin", "0.1.0")) {
		t.Fatal("schedule must go, <data> must stay")
	}
}

func TestUninstallKeepsDataWhenPluginListFails(t *testing.T) {
	_, p := uninstallFixture(t)
	agentsInstalled(t, p)
	fakeClaudeCLI(t, ownPlugins, "plugin list")
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall"}, &out, &errb); code != 1 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if !has(filepath.Join(p.Data, "agents.json")) || !has(filepath.Join(p.Data, "bin", "0.1.0")) {
		t.Fatal("<data> must stay")
	}
}

func TestUninstallRerunAfterMarketplaceFailureFinishes(t *testing.T) {
	_, p := uninstallFixture(t)
	agentsInstalled(t, p)
	logp := fakeClaudeCLI(t, ownPlugins, "plugin marketplace remove")
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "uninstall"}, &out, &errb); code != 1 {
		t.Fatalf("code %d", code)
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc["agents"] == nil || doc["error"] == nil {
		t.Fatalf("%v %s", err, out.String())
	}
	if !strings.Contains(readLines(t, logp), "plugin marketplace remove") || !has(filepath.Join(p.Data, "agents.json")) {
		t.Fatal("step did not run or <data> was deleted")
	}
	logp = fakeClaudeCLI(t, "[]", "")
	out.Reset()
	if code := Main([]string{"uninstall"}, &out, &errb); code != 0 {
		t.Fatalf("rerun code %d %s", code, errb.String())
	}
	if got := strings.TrimSpace(readLines(t, logp)); got != "plugin marketplace remove als0x-sapulidi" {
		t.Fatalf("rerun repeated finished steps: %q", got)
	}
	if has(filepath.Join(p.Data, "plugin")) || has(filepath.Join(p.Data, "bin")) {
		t.Fatal("rerun left the plugin folder or the binaries")
	}
}

func TestUninstallKeepsDataWhenClaudeMissing(t *testing.T) {
	be, p := uninstallFixture(t)
	agentsInstalled(t, p)
	t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall"}, &out, &errb); code != 1 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if len(be.installed) != 0 || !has(filepath.Join(p.Data, "plugin")) || !has(filepath.Join(p.Data, "bin", "0.1.0")) {
		t.Fatal("schedule must go, <data> must stay")
	}
}

func TestUninstallUsage(t *testing.T) {
	uninstallFixture(t)
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall", "--force"}, &out, &errb); code != 2 {
		t.Fatalf("code %d", code)
	}
}
