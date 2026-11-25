package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// selfEnv is updateEnv with version 0.3.0 installed and one TLS server for the release API and the downloads.
type selfEnv struct {
	*updateEnv
	data             string
	tag, binary      string
	requests, assets int
}

func newSelfEnv(t *testing.T, routes ...string) *selfEnv {
	t.Helper()
	e := &selfEnv{updateEnv: newUpdateEnv(t), tag: "v0.4.0", binary: "new"}
	e.data = filepath.Join(e.home, ".local", "share", "sapulidi")
	writeFile(t, filepath.Join(e.data, "bin", "0.3.0", "sapulidi"), "old")
	if err := os.Symlink("0.3.0", filepath.Join(e.data, "bin", "current")); err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		writeFile(t, filepath.Join(e.state, "installed-by", r), "")
	}
	asset := "sapulidi_0.4.0_" + runtime.GOOS + "_" + runtime.GOARCH
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.requests++
		switch r.URL.Path {
		case "/repos/als0x/sapulidi/releases/latest":
			fmt.Fprintf(w, `{"tag_name":%q}`, e.tag)
		case "/rel/" + asset:
			e.assets++
			fmt.Fprint(w, e.binary)
		case "/raw/checksums.txt":
			e.assets++
			fmt.Fprintf(w, "%x  %s\n", sha256.Sum256([]byte("new")), asset)
		default:
			w.WriteHeader(404)
		}
	}))
	oldBase, oldClient := updateAPIBase, updateClient
	updateAPIBase, updateClient = s.URL, s.Client()
	t.Cleanup(func() { updateAPIBase, updateClient = oldBase, oldClient; s.Close() })
	t.Setenv("SAPULIDI_RELEASE_BASE", s.URL+"/rel")
	t.Setenv("SAPULIDI_RAW_BASE", s.URL+"/raw")
	withTerminal(t, false, "")
	return e
}

func (e *selfEnv) current(t *testing.T) string {
	t.Helper()
	target, err := os.Readlink(filepath.Join(e.data, "bin", "current"))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// untouched fails unless nothing was downloaded and current still points at the old version.
func (e *selfEnv) untouched(t *testing.T) {
	t.Helper()
	if e.assets != 0 || e.current(t) != "0.3.0" {
		t.Fatalf("downloads %d, current %s", e.assets, e.current(t))
	}
}

func TestUpdateYesInstallsTheNewerRelease(t *testing.T) {
	e := newSelfEnv(t, "install.sh")
	out, errs, code := output(t, "update", "--yes")
	if code != 0 || !strings.Contains(out, "0.3.0 → v0.4.0") || !strings.Contains(out, "updated to v0.4.0") {
		t.Fatalf("code %d %q %q", code, out, errs)
	}
	if e.current(t) != "0.4.0" {
		t.Fatalf("current %s", e.current(t))
	}
}

func TestUpdateAsksOnTheTerminal(t *testing.T) {
	for typed, want := range map[string]string{"y\n": "0.4.0", "n\n": "0.3.0", "\n": "0.3.0"} {
		e := newSelfEnv(t, "install.sh")
		withTerminal(t, true, typed)
		_, errs, code := output(t, "update")
		if e.current(t) != want || (code == 0) != (want == "0.4.0") {
			t.Errorf("typed %q: code %d current %s %q", typed, code, e.current(t), errs)
		}
		if want == "0.3.0" && e.assets != 0 {
			t.Errorf("typed %q: downloaded", typed)
		}
	}
}

func TestUpdateWithoutATerminalIsRefused(t *testing.T) {
	e := newSelfEnv(t, "install.sh")
	_, errs, code := output(t, "update")
	if code != 1 || !strings.Contains(errs, "--yes") || e.requests != 0 {
		t.Fatalf("code %d requests %d %q", code, e.requests, errs)
	}
	e.untouched(t)
}

func TestUpdateExtraArgumentIsAUsageError(t *testing.T) {
	e := newSelfEnv(t, "install.sh")
	for _, args := range [][]string{{"update", "extra"}, {"update", "--check-only", "extra"}} {
		if _, errs, code := output(t, args...); code != 2 || errs == "" || e.requests != 0 {
			t.Errorf("%v: code %d requests %d %q", args, code, e.requests, errs)
		}
	}
}

func TestUpdateWithTheSameTagDoesNothing(t *testing.T) {
	e := newSelfEnv(t, "install.sh")
	e.tag = "v0.3.0"
	out, errs, code := output(t, "update", "--yes")
	if code != 0 || !strings.Contains(out, "nothing to do") {
		t.Fatalf("code %d %q %q", code, out, errs)
	}
	e.untouched(t)
}

func TestUpdateChecksumMismatchSaysWhy(t *testing.T) {
	e := newSelfEnv(t, "install.sh")
	e.binary = "tampered"
	_, errs, code := output(t, "update", "--yes")
	if code != 1 || !strings.Contains(errs, "checksum mismatch") || e.current(t) != "0.3.0" {
		t.Fatalf("code %d %q current %s", code, errs, e.current(t))
	}
	e.wantLog(t, "update-failed")
}

func TestUpdatePluginInstallPrintsThePluginCommand(t *testing.T) {
	e := newSelfEnv(t, "plugin")
	out, errs, code := output(t, "update") // no terminal needed: nothing is installed
	if code != 0 || !strings.Contains(out, "/plugin update sapulidi@als0x-sapulidi") {
		t.Fatalf("code %d %q %q", code, out, errs)
	}
	e.untouched(t)
}

func TestUpdateWithoutAMarkerIsRefused(t *testing.T) {
	e := newSelfEnv(t)
	_, errs, code := output(t, "update", "--yes")
	if code != 1 || !strings.Contains(errs, "https://github.com/als0x/sapulidi/releases") || e.requests != 0 {
		t.Fatalf("code %d requests %d %q", code, e.requests, errs)
	}
	e.untouched(t)
}

func TestUpdateObeysUpdateCheckOff(t *testing.T) {
	e := newSelfEnv(t, "install.sh")
	e.config(t, "[update]\ncheck = false\n")
	_, errs, code := output(t, "update", "--yes")
	if code != 1 || !strings.Contains(errs, "update.check is false") || e.requests != 0 {
		t.Fatalf("code %d requests %d %q", code, e.requests, errs)
	}
}

func TestUpdateJSONIsOneDocument(t *testing.T) {
	newSelfEnv(t, "install.sh")
	out, errs, code := output(t, "--json", "update", "--yes")
	var doc struct {
		Updated                  bool
		Current, Latest, Running string
	}
	if err := json.Unmarshal([]byte(out), &doc); code != 0 || err != nil || !doc.Updated ||
		doc.Latest != "v0.4.0" || doc.Running != "0.3.0" || doc.Current != "0.4.0" {
		t.Fatalf("code %d %q %q %v", code, out, errs, err)
	}
}

// newAutoEnv is selfEnv with a live host, so a scheduled run is not taken for an uninstalled one.
func newAutoEnv(t *testing.T, auto bool, routes ...string) *selfEnv {
	t.Helper()
	e := newSelfEnv(t, routes...)
	e.config(t, fmt.Sprintf("[update]\nauto = %v\n", auto))
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	st.Heartbeat("claude-code", "0.3.0")
	return e
}

func scheduled(t *testing.T) (stdout string) {
	t.Helper()
	out, errs, code := output(t, "run", "--trigger", "schedule")
	if code != 0 {
		t.Fatalf("run: code %d %q", code, errs)
	}
	return out
}

func TestScheduledRunUpdatesOnceADay(t *testing.T) {
	e := newAutoEnv(t, true, "install.sh")
	out := scheduled(t)
	if e.current(t) != "0.4.0" || strings.Contains(out, "v0.4.0") || strings.Count(out, "\n") != 1 {
		t.Fatalf("current %s output %q", e.current(t), out)
	}
	e.wantLog(t, `"update-auto"`)
	n := e.requests
	scheduled(t)
	if e.requests != n {
		t.Fatalf("a second run asked again: %d requests, %d before", e.requests, n)
	}
	const line = "Sapu Lidi updated itself to v0.4.0."
	if out, _, _ := output(t, "check", "--host", "claude-code"); strings.Count(out, line) != 1 {
		t.Fatalf("check: %q", out)
	}
	if out, _, _ := output(t, "check", "--host", "claude-code"); strings.Contains(out, line) {
		t.Fatalf("said twice: %q", out)
	}
}

func TestScheduledRunRetriesTheNextDayAndStaysSilent(t *testing.T) {
	e := newAutoEnv(t, true, "install.sh")
	e.binary = "tampered"
	out, errs, code := output(t, "--json", "run", "--trigger", "schedule")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); code != 0 || errs != "" || err != nil || e.current(t) != "0.3.0" {
		t.Fatalf("code %d %q %q %v", code, out, errs, err)
	}
	e.wantLog(t, "update-auto-failed")
	e.wantLog(t, "checksum mismatch")
	if out, _, _ := output(t, "check", "--host", "claude-code"); strings.Contains(out, "updated itself") {
		t.Fatalf("check: %q", out)
	}
	e.binary = "new"
	scheduled(t)
	if e.current(t) != "0.3.0" {
		t.Fatal("retried within a day")
	}
	claim := filepath.Join(e.state, "auto-update", "update.claim")
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(claim, old, old); err != nil {
		t.Fatal(err)
	}
	scheduled(t)
	if e.current(t) != "0.4.0" {
		t.Fatalf("not retried: current %s", e.current(t))
	}
}

func TestScheduledRunDoesNotUpdate(t *testing.T) {
	for name, c := range map[string]struct {
		auto     bool
		routes   []string
		args     []string
		setup    func(*testing.T, *selfEnv)
		requests int
	}{
		"auto off":      {false, []string{"install.sh"}, nil, nil, 0},
		"plugin only":   {true, []string{"plugin"}, nil, nil, 0},
		"no marker":     {true, nil, nil, nil, 0},
		"manual run":    {true, []string{"install.sh"}, []string{"run"}, nil, 0},
		"tui run":       {true, []string{"install.sh"}, []string{"run", "--trigger", "tui"}, nil, 0},
		"dry run":       {true, []string{"install.sh"}, []string{"run", "--trigger", "schedule", "--dry-run"}, nil, 0},
		"on a terminal": {true, []string{"install.sh"}, nil, func(t *testing.T, _ *selfEnv) { withTerminal(t, true, "") }, 0},
		"paused": {true, []string{"install.sh"}, nil, func(t *testing.T, _ *selfEnv) {
			if _, errs, code := output(t, "pause"); code != 0 {
				t.Fatal(errs)
			}
		}, 0},
		"check off": {true, []string{"install.sh"}, nil, func(t *testing.T, e *selfEnv) {
			e.config(t, "[update]\nauto = true\ncheck = false\n")
		}, 0},
		"same tag": {true, []string{"install.sh"}, nil, func(_ *testing.T, e *selfEnv) { e.tag = "v0.3.0" }, 1},
	} {
		t.Run(name, func(t *testing.T) {
			e := newAutoEnv(t, c.auto, c.routes...)
			if c.setup != nil {
				c.setup(t, e)
			}
			args := c.args
			if args == nil {
				args = []string{"run", "--trigger", "schedule"}
			}
			if _, errs, code := output(t, args...); code != 0 {
				t.Fatalf("code %d %q", code, errs)
			}
			if e.requests != c.requests {
				t.Fatalf("%d requests, want %d", e.requests, c.requests)
			}
			e.untouched(t)
			if out, _, _ := output(t, "check", "--host", "claude-code"); strings.Contains(out, "updated itself") {
				t.Fatalf("check: %q", out)
			}
		})
	}
}
