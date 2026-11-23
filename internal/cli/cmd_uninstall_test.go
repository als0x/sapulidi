package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
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

func TestUninstallUsage(t *testing.T) {
	uninstallFixture(t)
	var out, errb bytes.Buffer
	if code := Main([]string{"uninstall", "--force"}, &out, &errb); code != 2 {
		t.Fatalf("code %d", code)
	}
}
