package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/testutil"
)

// binPath is the sapulidi binary built once by TestMain, before any test changes HOME (the Go build cache
// lives under it).
var binPath string

var consentRe = regexp.MustCompile(`consent recorded for [0-9]+ rules`)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sapulidi-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "sapulidi")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binPath, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	// The binary shows desktop notifications through osascript / notify-send; the tests must never pop one up
	// on the machine running them, so those two names resolve to do-nothing stubs first on PATH.
	stubDir = filepath.Join(dir, "stubs")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, name := range []string{"osascript", "notify-send"} {
		if err := os.WriteFile(filepath.Join(stubDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// stubDir holds the do-nothing notifier stubs that every e2e run gets first on PATH.
var stubDir string

type e2e struct {
	t    *testing.T
	home string
	tmp  string
}

// newE2E makes a temp home, with the temp dir and the agent scratch base inside it and a config that keeps
// the low-disk warning from depending on the machine running the tests.
func newE2E(t *testing.T) *e2e {
	t.Helper()
	home := testutil.TempHome(t)
	tmp := filepath.Join(home, "tmp")
	for _, d := range []string{tmp, filepath.Join(home, "scratch")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(home, ".config", "sapulidi", "config.toml")
	if err := os.WriteFile(cfg, []byte("low_disk_gb = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &e2e{t: t, home: home, tmp: tmp}
}

// run runs the built binary with the test hooks pointing inside the temp home.
func (e *e2e) run(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = e.home
	cmd.Env = append(os.Environ(),
		"SAPULIDI_TEST=1",
		"SAPULIDI_TEST_NO_CTIME=1",
		"SAPULIDI_TEST_TEMPDIR="+e.tmp,
		"SAPULIDI_TEST_SCRATCH="+filepath.Join(e.home, "scratch"),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		e.t.Fatalf("running %v: %v", args, err)
	}
	return so.String(), se.String(), code
}

// mkTemp makes a directory with a file in the temp dir whose newest mtime is age ago.
func (e *e2e) mkTemp(name string, age time.Duration) string {
	e.t.Helper()
	p := filepath.Join(e.tmp, name)
	testutil.Touch(e.t, filepath.Join(p, "obj"), age)
	testutil.Touch(e.t, p, age)
	return p
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestE2ENoConsentDryRun(t *testing.T) {
	e := newE2E(t)
	old := e.mkTemp("go-build1", 5*time.Hour)
	out, errs, code := e.run("run")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errs)
	}
	if !strings.Contains(out, "no consent") || !strings.Contains(out, "1 would be removed") {
		t.Fatalf("summary %q", out)
	}
	if !exists(old) {
		t.Fatal("a run before consent removed something")
	}
}

func TestE2ESetupYesThenRunRemoves(t *testing.T) {
	e := newE2E(t)
	old := e.mkTemp("go-build1", 5*time.Hour)

	out, errs, code := e.run("setup")
	if code != 0 || !strings.Contains(out, "Nothing is recorded yet") || !strings.Contains(out, "sapulidi setup --yes") {
		t.Fatalf("setup without --yes: exit %d %q %q", code, out, errs)
	}
	if _, _, code := e.run("run"); code != 0 || !exists(old) {
		t.Fatal("setup without --yes must record nothing")
	}

	out, errs, code = e.run("setup", "--yes")
	if code != 0 || !consentRe.MatchString(out) || !strings.Contains(out, "sapulidi schedule install") {
		t.Fatalf("setup --yes: exit %d %q %q", code, out, errs)
	}
	out, errs, code = e.run("run")
	if code != 0 || !strings.Contains(out, "removed 1 ") {
		t.Fatalf("run: exit %d %q %q", code, out, errs)
	}
	if exists(old) {
		t.Fatal("old go-build still there after setup --yes and run")
	}
}

func TestE2EYoungKept(t *testing.T) {
	e := newE2E(t)
	young := e.mkTemp("go-build-new", 10*time.Minute)
	if _, _, code := e.run("setup", "--yes"); code != 0 {
		t.Fatal("setup failed")
	}
	out, _, code := e.run("run")
	if code != 0 || !strings.Contains(out, "removed 0 ") || !strings.Contains(out, "kept 1") {
		t.Fatalf("run: exit %d %q", code, out)
	}
	if !exists(young) {
		t.Fatal("a young directory was removed")
	}
}

func TestE2ECheckSilentWhenFine(t *testing.T) {
	e := newE2E(t)
	if _, _, code := e.run("setup", "--yes"); code != 0 {
		t.Fatal("setup failed")
	}
	out, errs, code := e.run("check", "--host", "claude-code", "--host-version", "9.9.9")
	if code != 0 || out != "" || errs != "" {
		t.Fatalf("check: exit %d stdout %q stderr %q", code, out, errs)
	}
	hb := filepath.Join(e.home, ".local", "state", "sapulidi", "hosts", "claude-code")
	if b, err := os.ReadFile(hb); err != nil || strings.TrimSpace(string(b)) != "9.9.9" {
		t.Fatalf("heartbeat %q %v", b, err)
	}
}

func TestE2ECheckSaysNotSetUp(t *testing.T) {
	e := newE2E(t)
	out, _, code := e.run("check")
	want := "sapulidi is installed but not set up: run /sapulidi setup\n"
	if code != 0 || out != want {
		t.Fatalf("check: exit %d %q", code, out)
	}
}

func TestE2EJSONOutputsOneDocument(t *testing.T) {
	e := newE2E(t)
	e.mkTemp("go-build1", 5*time.Hour)
	for _, args := range [][]string{
		{"check"}, {"plan"}, {"report"}, {"setup"}, {"run", "--dry-run"},
		{"setup", "--yes"}, {"check"}, {"consent"}, {"consent", "--yes"}, {"run"}, {"plan", "--all"},
	} {
		out, errs, code := e.run(append([]string{"--json"}, args...)...)
		if code != 0 {
			t.Fatalf("%v: exit %d %q", args, code, errs)
		}
		dec := json.NewDecoder(strings.NewReader(out))
		var v map[string]any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("%v: stdout is not JSON: %q", args, out)
		}
		if _, err := dec.Token(); err != io.EOF {
			t.Fatalf("%v: more than one JSON document: %q", args, out)
		}
	}
}

func TestE2ECorruptConfig(t *testing.T) {
	e := newE2E(t)
	cfg := filepath.Join(e.home, ".config", "sapulidi", "config.toml")
	if err := os.WriteFile(cfg, []byte("this is = = not toml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const msg = "is not valid TOML: nothing will run until it is fixed"
	for _, cmd := range []string{"run", "plan"} {
		out, errs, code := e.run(cmd)
		if code != 0 || !strings.Contains(out, "config file "+cfg+" "+msg) {
			t.Fatalf("%s: exit %d %q %q", cmd, code, out, errs)
		}
	}
	out, _, _ := e.run("--json", "plan")
	var doc struct {
		Invalid []string `json:"invalid_config"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Invalid) != 1 || doc.Invalid[0] != cfg {
		t.Fatalf("plan --json: %q %v", out, err)
	}
	out, _, _ = e.run("--json", "run")
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Invalid) != 1 {
		t.Fatalf("run --json: %q %v", out, err)
	}
	out, _, code := e.run("check")
	if code != 0 || !strings.Contains(out, msg) {
		t.Fatalf("check: exit %d %q", code, out)
	}
	out, errs, code := e.run("setup", "--yes")
	if code != 1 || !strings.Contains(errs, msg) || strings.Contains(out, "consent recorded") {
		t.Fatalf("setup --yes: exit %d %q %q", code, out, errs)
	}
	if exists(filepath.Join(e.home, ".local", "state", "sapulidi", "consent.json")) {
		t.Fatal("setup --yes recorded consent on a corrupt config")
	}
	out, _, code = e.run("--json", "setup", "--yes")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Fatalf("setup --yes --json: exit %d %q", code, out)
	}
}

func TestE2ESetupYesRefusesWhenConsentExists(t *testing.T) {
	e := newE2E(t)
	if _, _, code := e.run("setup", "--yes"); code != 0 {
		t.Fatal("first setup --yes failed")
	}
	before, err := os.ReadFile(filepath.Join(e.home, ".local", "state", "sapulidi", "consent.json"))
	if err != nil {
		t.Fatal(err)
	}
	out, errs, code := e.run("setup", "--yes")
	want := "consent already recorded; run sapulidi consent to review the 0 waiting rules"
	if code != 1 || !strings.Contains(errs, want) || strings.Contains(out, "consent recorded") {
		t.Fatalf("second setup --yes: exit %d %q %q", code, out, errs)
	}
	after, _ := os.ReadFile(filepath.Join(e.home, ".local", "state", "sapulidi", "consent.json"))
	if string(before) != string(after) {
		t.Fatal("the consent record was rewritten")
	}
}

func TestE2ECheckPeeksFactsWithoutHost(t *testing.T) {
	e := newE2E(t)
	if _, _, code := e.run("setup", "--yes"); code != 0 {
		t.Fatal("setup failed")
	}
	facts := filepath.Join(e.home, ".local", "state", "sapulidi", "facts.json")
	if err := os.WriteFile(facts, []byte(`{"k":{"text":"worktree kept for unsaved work","undelivered":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // a manual check only peeks: the fact stays
		out, _, _ := e.run("check")
		if !strings.Contains(out, "worktree kept for unsaved work") {
			t.Fatalf("peek %d: %q", i, out)
		}
	}
	out, _, _ := e.run("--json", "check")
	if !strings.Contains(out, "worktree kept for unsaved work") {
		t.Fatalf("json check: %q", out)
	}
	out, _, _ = e.run("check", "--host", "claude-code")
	if !strings.Contains(out, "worktree kept for unsaved work") {
		t.Fatalf("hook check: %q", out)
	}
	out, _, _ = e.run("check", "--host", "claude-code")
	if out != "" {
		t.Fatalf("the hook check must consume the fact: %q", out)
	}
}

func TestE2EJSONErrorsOnStdout(t *testing.T) {
	e := newE2E(t)
	out, _, code := e.run("--json", "run", "--budget", "nonsense")
	if code != 2 || !strings.Contains(out, `"error"`) {
		t.Fatalf("usage error: exit %d %q", code, out)
	}
	out, _, code = e.run("--json", "plan", "extra")
	if code != 2 || !strings.Contains(out, `"error"`) {
		t.Fatalf("unexpected argument: exit %d %q", code, out)
	}
	out, _, code = e.run("--json", "consent", "--yes")
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Fatalf("consent before setup: exit %d %q", code, out)
	}
}

func TestE2EConsentListsAndConfirmsNewRules(t *testing.T) {
	e := newE2E(t)
	if _, _, code := e.run("consent", "--yes"); code != 1 {
		t.Fatal("consent before setup must fail")
	}
	if _, _, code := e.run("setup", "--yes"); code != 0 {
		t.Fatal("setup failed")
	}
	out, _, code := e.run("consent")
	if code != 0 || !strings.Contains(out, "no rules are waiting") {
		t.Fatalf("consent: %d %q", code, out)
	}
	// A user pack adds an auto rule after setup: it waits, check says so, consent --yes confirms it.
	packs := filepath.Join(e.home, ".config", "sapulidi", "packs")
	if err := os.MkdirAll(packs, 0o700); err != nil {
		t.Fatal(err)
	}
	pack := "pack = \"mine\"\n[[rule]]\nid = \"cache1\"\nrev = 1\ncategory = \"temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"mytool-*\"]\nmin_age = \"2h\"\n"
	if err := os.WriteFile(filepath.Join(packs, "mine.toml"), []byte(pack), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, _ = e.run("check")
	if !strings.Contains(out, "1 new rule(s) waiting for your OK") {
		t.Fatalf("check: %q", out)
	}
	old := e.mkTemp("mytool-1", 5*time.Hour)
	if _, _, code := e.run("run"); code != 0 || !exists(old) {
		t.Fatal("a rule that was not confirmed ran")
	}
	out, _, _ = e.run("consent", "--yes")
	if !strings.Contains(out, "consent recorded for 1 rules") {
		t.Fatalf("consent --yes: %q", out)
	}
	if _, _, code := e.run("run"); code != 0 || exists(old) {
		t.Fatal("the confirmed rule did not run")
	}
}
