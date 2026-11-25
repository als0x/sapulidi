package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/testutil"
	"github.com/als0x/sapulidi/internal/update"
	"github.com/als0x/sapulidi/internal/version"
)

// updateEnv is a temp home with a release build (version 0.3.0, a repository) and a counted child starter.
type updateEnv struct {
	home, state string
	started     int
}

func newUpdateEnv(t *testing.T) *updateEnv {
	t.Helper()
	home := testutil.TempHome(t)
	e := &updateEnv{home: home, state: filepath.Join(home, ".local", "state", "sapulidi")}
	oldRepo, oldVersion, oldStart, oldFree := version.Repo, version.Version, startUpdateCheck, freeBytes
	version.Repo, version.Version = "als0x/sapulidi", "0.3.0"
	startUpdateCheck = func() error { e.started++; return nil }
	freeBytes = func(string) (uint64, error) { return 1 << 50, nil }
	t.Cleanup(func() {
		version.Repo, version.Version, startUpdateCheck, freeBytes = oldRepo, oldVersion, oldStart, oldFree
	})
	return e
}

func (e *updateEnv) cache(t *testing.T, tag string, age time.Duration) {
	t.Helper()
	if err := update.SaveCache(e.state, update.Cache{Tag: tag, CheckedAt: time.Now().Add(-age)}); err != nil {
		t.Fatal(err)
	}
}

func (e *updateEnv) config(t *testing.T, body string) {
	t.Helper()
	writeFile(t, filepath.Join(e.home, ".config", "sapulidi", "config.toml"), body)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// output is runCLI with the output first.
func output(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	code, stdout, stderr = runCLI(t, args...)
	return stdout, stderr, code
}

func TestCheckStartsNoChildWhileTheCacheIsFresh(t *testing.T) {
	e := newUpdateEnv(t)
	e.cache(t, "v0.3.0", 23*time.Hour)
	if _, errs, code := output(t, "check"); code != 0 || e.started != 0 {
		t.Fatalf("code %d started %d %q", code, e.started, errs)
	}
}

func TestCheckStartsOneChildPerDay(t *testing.T) {
	for name, age := range map[string]time.Duration{"missing": -1, "stale": 25 * time.Hour, "corrupt": -2} {
		t.Run(name, func(t *testing.T) {
			e := newUpdateEnv(t)
			switch age {
			case -1:
			case -2:
				writeFile(t, filepath.Join(e.state, "update.json"), "{")
			default:
				e.cache(t, "v0.3.0", age)
			}
			for i := 0; i < 3; i++ {
				if _, errs, code := output(t, "check"); code != 0 {
					t.Fatalf("code %d %q", code, errs)
				}
			}
			if e.started != 1 {
				t.Fatalf("started %d children", e.started)
			}
			if _, err := os.Stat(filepath.Join(e.state, "update.claim")); err != nil {
				t.Fatalf("no claim: %v", err)
			}
			c, err := update.LoadCache(e.state)
			if age > 0 && (err != nil || c.Tag != "v0.3.0" || time.Since(c.CheckedAt) < 24*time.Hour) {
				t.Fatalf("check wrote the cache: %+v %v", c, err)
			}
		})
	}
}

func TestConcurrentChecksStartOneChildAndKeepItsTag(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		for range 10 {
			e := newUpdateEnv(t)
			e.cache(t, "v0.3.0", 48*time.Hour)
			if claimed { // yesterday's claim is there to be replaced
				claim := filepath.Join(e.state, "update.claim")
				writeFile(t, claim, "")
				old := time.Now().Add(-25 * time.Hour)
				if err := os.Chtimes(claim, old, old); err != nil {
					t.Fatal(err)
				}
			}
			var started atomic.Int32
			startUpdateCheck = func() error { // a child that answers at once
				started.Add(1)
				return update.SaveCache(e.state, update.Cache{Tag: "v0.5.0", CheckedAt: time.Now()})
			}
			var wg sync.WaitGroup
			begin := make(chan struct{})
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-begin
					var out, errb bytes.Buffer
					if code := Main([]string{"check"}, &out, &errb); code != 0 {
						t.Errorf("code %d %q", code, errb.String())
					}
				}()
			}
			close(begin)
			wg.Wait()
			if n := started.Load(); n != 1 {
				t.Fatalf("claimed %v: %d children", claimed, n)
			}
			if c, err := update.LoadCache(e.state); err != nil || c.Tag != "v0.5.0" {
				t.Fatalf("claimed %v: the child's tag was overwritten: %+v %v", claimed, c, err)
			}
		}
	}
}

func TestCheckStartsNoChildWhenDisabled(t *testing.T) {
	for name, setup := range map[string]func(*testing.T, *updateEnv){
		"update.check off": func(t *testing.T, e *updateEnv) { e.config(t, "[update]\ncheck = false\n") },
		"invalid config":   func(t *testing.T, e *updateEnv) { e.config(t, "[update\ncheck = false\n") },
		"no repository":    func(*testing.T, *updateEnv) { version.Repo = "" },
	} {
		t.Run(name, func(t *testing.T) {
			e := newUpdateEnv(t)
			e.cache(t, "v0.4.0", 48*time.Hour)
			setup(t, e)
			out, errs, code := output(t, "check")
			if code != 0 || e.started != 0 || strings.Contains(out, "v0.4.0") {
				t.Fatalf("code %d started %d %q %q", code, e.started, out, errs)
			}
		})
	}
}

// A config file that cannot be read may hold update.check = false.
func TestUnreadableConfigMeansNoCheckAndALogLine(t *testing.T) {
	e := newUpdateEnv(t)
	n := api(t, 200, `{"tag_name":"v0.4.0"}`)
	writeFile(t, filepath.Join(e.home, ".config", "sapulidi", "config.toml", "x"), "")
	if _, errs, code := output(t, "check"); code != 0 || e.started != 0 {
		t.Fatalf("check: code %d started %d %q", code, e.started, errs)
	}
	out, errs, code := output(t, "update", "--check-only", "--quiet")
	if code != 0 || out != "" || errs != "" || *n != 0 {
		t.Fatalf("update: code %d requests %d %q %q", code, *n, out, errs)
	}
	b, err := os.ReadFile(filepath.Join(e.state, "log.jsonl"))
	if err != nil || !strings.Contains(string(b), "update-check-failed") {
		t.Fatalf("log %q %v", b, err)
	}
}

func TestUpdateCheckChildIsDetached(t *testing.T) {
	cmd := updateCheckCommand("/opt/sapulidi")
	if got := strings.Join(cmd.Args, " "); got != "/opt/sapulidi update --check-only --quiet" {
		t.Errorf("args %q", got)
	}
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil { // nil is the null device
		t.Error("the child must not share the session's stdin, stdout or stderr")
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid || cmd.Dir != "/" {
		t.Errorf("not detached: %+v dir %q", cmd.SysProcAttr, cmd.Dir)
	}
}

func TestCheckStartsNoChildWhenTheStartCannotBeRecorded(t *testing.T) {
	e := newUpdateEnv(t)
	// An old directory where the claim file belongs cannot be replaced.
	claim := filepath.Join(e.state, "update.claim")
	writeFile(t, filepath.Join(claim, "x"), "")
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(claim, old, old); err != nil {
		t.Fatal(err)
	}
	if _, errs, code := output(t, "check"); code != 0 || errs != "" || e.started != 0 {
		t.Fatalf("code %d started %d %q", code, e.started, errs)
	}
	e.wantLog(t, "update-check-not-started")
}

func (e *updateEnv) wantLog(t *testing.T, sub string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.state, "log.jsonl"))
	if err != nil || !strings.Contains(string(b), sub) {
		t.Fatalf("log lacks %q: %q %v", sub, b, err)
	}
}

func TestCheckLogsAStarterThatFails(t *testing.T) {
	e := newUpdateEnv(t)
	startUpdateCheck = func() error { return errors.New("no fork") }
	out, errs, code := output(t, "check")
	if code != 0 || errs != "" || strings.Contains(out, "no fork") {
		t.Fatalf("code %d %q %q", code, out, errs)
	}
	e.wantLog(t, "update-check-not-started")
	e.wantLog(t, "no fork")
}

func TestCheckDoesNotWaitForABlockedStarter(t *testing.T) {
	newUpdateEnv(t)
	release := make(chan struct{})
	done := make(chan struct{})
	startUpdateCheck = func() error { defer close(done); <-release; return nil }
	start := time.Now()
	_, errs, code := output(t, "check")
	d := time.Since(start)
	close(release)
	<-done
	if code != 0 || d > 2*time.Second {
		t.Fatalf("code %d took %v %q", code, d, errs)
	}
}

func TestCheckOutputUnchangedWithoutANewerTag(t *testing.T) {
	e := newUpdateEnv(t)
	base, _, _ := output(t, "check")
	for _, tag := range []string{"v0.3.0", "v0.2.0", "v0.4.0-rc1"} {
		e.cache(t, tag, time.Hour)
		if out, _, _ := output(t, "check"); out != base {
			t.Fatalf("%s: %q, want %q", tag, out, base)
		}
	}
}

func TestCheckPrintsTheNoticeLast(t *testing.T) {
	e := newUpdateEnv(t)
	base, _, _ := output(t, "check")
	writeFile(t, filepath.Join(e.state, "installed-by", "plugin"), "")
	e.cache(t, "v0.4.0", time.Hour)
	for host, want := range map[string]string{
		"":            "/plugin update sapulidi@als0x-sapulidi",
		"claude-code": "/plugin update sapulidi@als0x-sapulidi",
		"codex":       "update the plugin in your agent tool",
	} {
		args := []string{"check"}
		if host != "" {
			args = append(args, "--host", host)
		}
		out, errs, code := output(t, args...)
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		last := lines[len(lines)-1]
		if code != 0 || !strings.HasPrefix(out, base) || !strings.HasPrefix(last, "Sapu Lidi v0.4.0") ||
			!strings.HasSuffix(last, want) {
			t.Errorf("host %q: code %d %q %q", host, code, out, errs)
		}
	}
	out, _, _ := output(t, "--json", "check")
	var doc struct{ Lines []string }
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Lines) == 0 ||
		!strings.Contains(doc.Lines[len(doc.Lines)-1], "v0.4.0") {
		t.Fatalf("json: %q %v", out, err)
	}
}

// api points update --check-only at a test server and counts its requests.
func api(t *testing.T, status int, body string) *int {
	t.Helper()
	n := new(int)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*n++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	old := updateAPIBase
	updateAPIBase = s.URL
	t.Cleanup(func() { updateAPIBase = old; s.Close() })
	return n
}

func TestUpdateCheckOnlySavesTheCache(t *testing.T) {
	e := newUpdateEnv(t)
	n := api(t, 200, `{"tag_name":"v0.4.0"}`)
	out, errs, code := output(t, "update", "--check-only")
	if code != 0 || *n != 1 || !strings.Contains(out, "v0.4.0") {
		t.Fatalf("code %d requests %d %q %q", code, *n, out, errs)
	}
	c, err := update.LoadCache(e.state)
	if err != nil || c.Tag != "v0.4.0" || time.Since(c.CheckedAt) > time.Minute {
		t.Fatalf("cache %+v %v", c, err)
	}
}

func TestUpdateCheckOnlyJSON(t *testing.T) {
	newUpdateEnv(t)
	api(t, 200, `{"tag_name":"v0.4.0"}`)
	out, errs, code := output(t, "--json", "update", "--check-only")
	var doc struct {
		Checked         bool
		Current, Latest string
		Newer           bool
	}
	if err := json.Unmarshal([]byte(out), &doc); code != 0 || err != nil || !doc.Checked || !doc.Newer ||
		doc.Latest != "v0.4.0" || doc.Current != "0.3.0" {
		t.Fatalf("code %d %q %q %v", code, out, errs, err)
	}
}

func TestUpdateCheckOnlyQuietPrintsNothing(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
	}{{"newer", 200, `{"tag_name":"v0.4.0"}`}, {"forbidden", 403, ""}, {"junk", 200, "junk"}} {
		for _, args := range [][]string{{"update", "--check-only", "--quiet"}, {"--json", "update", "--check-only", "--quiet"}} {
			t.Run(c.name, func(t *testing.T) {
				newUpdateEnv(t)
				api(t, c.status, c.body)
				out, errs, code := output(t, args...)
				if code != 0 || out != "" || errs != "" {
					t.Fatalf("code %d %q %q", code, out, errs)
				}
			})
		}
	}
}

func TestUpdateCheckOnlyFailureKeepsTheCacheAndLogs(t *testing.T) {
	e := newUpdateEnv(t)
	e.cache(t, "v0.3.5", 48*time.Hour)
	api(t, 429, "")
	if _, _, code := output(t, "update", "--check-only", "--quiet"); code != 0 {
		t.Fatalf("code %d", code)
	}
	if c, _ := update.LoadCache(e.state); c.Tag != "v0.3.5" {
		t.Fatalf("cache %+v", c)
	}
	b, err := os.ReadFile(filepath.Join(e.state, "log.jsonl"))
	if err != nil || !strings.Contains(string(b), "429") {
		t.Fatalf("log %q %v", b, err)
	}
	_, errs, code := output(t, "update", "--check-only")
	if code != 1 || !strings.Contains(errs, "429") {
		t.Fatalf("code %d %q", code, errs)
	}
}

func TestUpdateCheckOnlyQuietWithUnusableState(t *testing.T) {
	e := newUpdateEnv(t)
	api(t, 200, `{"tag_name":"v0.4.0"}`)
	t.Setenv("SAPULIDI_STATE", "relative/state")
	out, errs, code := output(t, "update", "--check-only", "--quiet")
	if code != 0 || out != "" || errs != "" {
		t.Fatalf("code %d %q %q (%s)", code, out, errs, e.state)
	}
}

func TestUpdateCheckOnlyMakesNoCallWhenDisabled(t *testing.T) {
	for name, setup := range map[string]func(*testing.T, *updateEnv){
		"update.check off": func(t *testing.T, e *updateEnv) { e.config(t, "[update]\ncheck = false\n") },
		"invalid config":   func(t *testing.T, e *updateEnv) { e.config(t, "[update\ncheck = false\n") },
		"no repository":    func(*testing.T, *updateEnv) { version.Repo = "" },
	} {
		t.Run(name, func(t *testing.T) {
			e := newUpdateEnv(t)
			n := api(t, 200, `{"tag_name":"v0.4.0"}`)
			setup(t, e)
			out, errs, code := output(t, "update", "--check-only")
			if code != 0 || *n != 0 || out == "" {
				t.Fatalf("code %d requests %d %q %q", code, *n, out, errs)
			}
			if c, _ := update.LoadCache(e.state); c.Tag != "" {
				t.Fatalf("cache %+v", c)
			}
		})
	}
}

func TestUpdateWithoutCheckOnlyNeedsATerminalOrYes(t *testing.T) {
	e := newUpdateEnv(t)
	writeFile(t, filepath.Join(e.state, "installed-by", "install.sh"), "")
	withTerminal(t, false, "")
	n := api(t, 200, `{"tag_name":"v0.4.0"}`)
	for _, args := range [][]string{{"update"}, {"update", "--quiet=false"}} {
		if _, errs, code := output(t, args...); code != 1 || !strings.Contains(errs, "--yes") {
			t.Errorf("%v: code %d %q", args, code, errs)
		}
	}
	if *n != 0 {
		t.Fatalf("%d requests", *n)
	}
}

func TestUpdateQuietCoversUsageErrors(t *testing.T) {
	newUpdateEnv(t)
	n := api(t, 200, `{"tag_name":"v0.4.0"}`)
	for _, args := range [][]string{
		{"update", "--quiet"},
		{"update", "-quiet"},
		{"update", "--quiet=true"},
		{"update", "--check-only", "--quiet", "extra"},
		{"update", "--check-only", "extra", "--quiet"},
		{"update", "--bogus", "--quiet"},
		{"update", "--quiet", "--check-only=maybe"},
		{"update", "--quiet", "--help"},
		{"--json", "update", "--bogus", "--quiet"},
		{"--json", "update", "--quiet"},
	} {
		if out, errs, code := output(t, args...); code != 0 || out != "" || errs != "" {
			t.Errorf("%v: code %d %q %q", args, code, out, errs)
		}
	}
	if *n != 0 {
		t.Fatalf("%d requests", *n)
	}
}
