package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/schedule"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/testutil"
)

type fakeBackend struct {
	installed map[string]bool
	warn      []string
	jobs      map[string]schedule.Job // the last job installed per label
	next      string                  // what Status reports as the next run
	failWith  error                   // Install fails with it
}

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) Install(j schedule.Job) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.installed[j.Label] = true
	if f.jobs != nil {
		f.jobs[j.Label] = j
	}
	return nil
}
func (f *fakeBackend) Remove(l string) error { delete(f.installed, l); return nil }
func (f *fakeBackend) Status(l string) (schedule.Status, error) {
	next := ""
	if f.installed[l] {
		next = f.next
	}
	return schedule.Status{Installed: f.installed[l], Warnings: f.warn, Next: next}, nil
}

func useFake(t *testing.T, f *fakeBackend) config.Paths {
	home := testutil.TempHome(t)
	p := config.Paths{Config: filepath.Join(home, "c", "config.toml"), Data: filepath.Join(home, "d"), State: filepath.Join(home, "s")}
	old := scheduleSetup
	scheduleSetup = func() (schedule.Backend, config.Paths, error) { return f, p, nil }
	t.Cleanup(func() { scheduleSetup = old })
	return p
}

func recordConsent(t *testing.T, p config.Paths) {
	t.Helper()
	st, err := state.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveConsent(&config.Consent{At: time.Now().UTC(), Rules: []string{"go/go-build@1"}}); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleInstallEverySetsAndStoresTheInterval(t *testing.T) {
	f := &fakeBackend{installed: map[string]bool{}, jobs: map[string]schedule.Job{}}
	p := useFake(t, f)
	if code, out, errs := run(t, "schedule", "install", "--every", "300s"); code != 0 || !strings.Contains(out, "installed") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	if got := f.jobs[schedule.LabelRun].Interval; got != 5*time.Minute {
		t.Fatalf("job interval %v", got)
	}
	cfg, _, _ := config.Load(p.Config)
	if cfg.Schedule.Interval != 5*time.Minute {
		t.Fatalf("stored %v", cfg.Schedule.Interval)
	}
	// Without the flag the stored value is used; with --every=<v> it is replaced.
	if code, _, errs := run(t, "schedule", "install"); code != 0 || f.jobs[schedule.LabelRun].Interval != 5*time.Minute {
		t.Fatalf("%d %s %v", code, errs, f.jobs[schedule.LabelRun].Interval)
	}
	if code, _, errs := run(t, "schedule", "install", "--every=1d"); code != 0 || f.jobs[schedule.LabelRun].Interval != 24*time.Hour {
		t.Fatalf("%d %s %v", code, errs, f.jobs[schedule.LabelRun].Interval)
	}
	t.Setenv("SAPULIDI_CONFIG", p.Config)
	if code, out, _ := run(t, "config"); code != 0 || !strings.Contains(out, "schedule.interval") || !strings.Contains(out, "(applied.toml)") {
		t.Fatalf("config does not show the setting:\n%s", out)
	}
}

func TestScheduleInstallEveryRefusals(t *testing.T) {
	f := &fakeBackend{installed: map[string]bool{}, jobs: map[string]schedule.Job{}}
	p := useFake(t, f)
	for _, args := range [][]string{
		{"schedule", "install", "--every", "4m"},
		{"schedule", "install", "--every", "5"},
		{"schedule", "install", "--every"},
		{"schedule", "install", "agent", "--every", "1h"},
		{"schedule", "remove", "--every", "1h"},
		{"schedule", "status", "--every", "1h"},
	} {
		if code, _, errs := run(t, args...); code != 2 || errs == "" {
			t.Errorf("%v: code %d %q", args, code, errs)
		}
	}
	if len(f.installed) != 0 {
		t.Fatalf("installed after a refusal: %v", f.installed)
	}
	if _, err := os.Stat(config.AppliedPath(p.Config)); err == nil {
		t.Fatal("a refused value was stored")
	}
	// The backend refusing (cron cannot do 90m) keeps the old value.
	_ = config.Apply(p, config.Change{Key: "schedule.interval", Value: "1h"})
	f.failWith = errors.New("cron cannot run every 90m; use 5 to 59 minutes, whole hours up to 23, 1d or 7d")
	code, _, errs := run(t, "schedule", "install", "--every", "90m")
	if code != 1 || !strings.Contains(errs, "cron cannot run every 90m") {
		t.Fatalf("%d %s", code, errs)
	}
	if cfg, _, _ := config.Load(p.Config); cfg.Schedule.Interval != time.Hour {
		t.Fatalf("stored %v after a failed install", cfg.Schedule.Interval)
	}
}

func TestScheduleInstallWithUnreadableConfigInstallsNothing(t *testing.T) {
	for _, args := range [][]string{{"schedule", "install"}, {"schedule", "install", "--every", "2h"}} {
		f := &fakeBackend{installed: map[string]bool{}, jobs: map[string]schedule.Job{}}
		p := useFake(t, f)
		if err := os.MkdirAll(filepath.Dir(p.Config), 0o700); err != nil {
			t.Fatal(err)
		}
		applied := config.AppliedPath(p.Config)
		body := "[schedule]\ninterval = \"7d\"\nthis is = = garbage\n"
		if err := os.WriteFile(applied, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, errs := run(t, args...)
		if code != 1 || !strings.Contains(errs, applied) {
			t.Errorf("%v: code %d %q", args, code, errs)
		}
		if len(f.installed) != 0 || len(f.jobs) != 0 {
			t.Errorf("%v: installed %v", args, f.installed)
		}
		if got, _ := os.ReadFile(applied); string(got) != body {
			t.Errorf("%v: applied.toml was changed:\n%s", args, got)
		}
	}
}

func TestScheduleFailedInstallRestoresTheStoredInterval(t *testing.T) {
	f := &fakeBackend{installed: map[string]bool{}, jobs: map[string]schedule.Job{}}
	p := useFake(t, f)
	f.failWith = errors.New("cron cannot run every 90m; use 5 to 59 minutes, whole hours up to 23, 1d or 7d")
	// No stored value before: none after, the effective value is the default from no file.
	if code, _, _ := run(t, "schedule", "install", "--every", "90m"); code != 1 {
		t.Fatalf("code %d", code)
	}
	if v, had, err := config.AppliedScheduleInterval(p); err != nil || had {
		t.Fatalf("a refused value stayed stored: %q %v %v", v, had, err)
	}
	if cfg, _, _ := config.Load(p.Config); cfg.Schedule.Interval != 30*time.Minute {
		t.Fatalf("%v", cfg.Schedule.Interval)
	}
	// A stored value before: the same one after.
	if err := config.Apply(p, config.Change{Key: "schedule.interval", Value: "2h"}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "schedule", "install", "--every", "90m"); code != 1 {
		t.Fatalf("code %d", code)
	}
	if v, had, _ := config.AppliedScheduleInterval(p); !had || v != "2h" {
		t.Fatalf("stored %q %v", v, had)
	}
}

func TestScheduleStatusTreatsAnUnreadableConsentAsNone(t *testing.T) {
	f := &fakeBackend{installed: map[string]bool{schedule.LabelRun: true}}
	p := useFake(t, f)
	recordConsent(t, p)
	consent := filepath.Join(p.State, "consent.json")
	for name, body := range map[string]string{"corrupt": "{not json", "empty": ""} {
		if err := os.WriteFile(consent, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, out, _ := run(t, "schedule", "status"); !strings.Contains(out, "dry runs only: no consent recorded") {
			t.Errorf("%s consent.json: no dry-run note in:\n%s", name, out)
		}
	}
}

func TestScheduleStatusShowsIntervalNextRunAndConsent(t *testing.T) {
	f := &fakeBackend{installed: map[string]bool{schedule.LabelRun: true}, next: "12m34s"}
	p := useFake(t, f)
	_ = config.Apply(p, config.Change{Key: "schedule.interval", Value: "2h"})
	_, out, _ := run(t, "schedule", "status")
	for _, want := range []string{"every 2h", "next run in 12m34s", "dry runs only: no consent recorded", "every 7d"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	// A job that is not installed has no next run and no consent note.
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if strings.Contains(l, schedule.LabelAgent) && (strings.Contains(l, "next run") || strings.Contains(l, "dry runs")) {
			t.Errorf("agent line: %s", l)
		}
	}
	recordConsent(t, p)
	_, out, _ = run(t, "schedule", "status")
	if strings.Contains(out, "dry runs only") {
		t.Fatalf("consent is recorded:\n%s", out)
	}
	f.next = ""
	if _, out, _ = run(t, "schedule", "status"); strings.Contains(out, "next run") {
		t.Fatalf("next run invented:\n%s", out)
	}
	_, js, _ := run(t, "--json", "schedule", "status")
	var got []struct {
		Interval string `json:"interval"`
		NextRun  string `json:"next_run"`
	}
	if err := json.Unmarshal([]byte(js), &got); err != nil || len(got) != 2 || got[0].Interval != "2h" || got[1].Interval != "7d" {
		t.Fatalf("%v %s", err, js)
	}
}

// TestScheduledRunSeesConsentOfInstallingShell: the shell exports XDG_*_HOME, setup records consent there, and the
// scheduler (which has no shell rc) must still resolve the same state, or it dry-runs for good.
func TestScheduledRunSeesConsentOfInstallingShell(t *testing.T) {
	home := testutil.TempHome(t)
	for _, k := range []string{"SAPULIDI_CONFIG", "SAPULIDI_STATE", "SAPULIDI_DATA"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg cfg"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg data"))
	f := &fakeBackend{installed: map[string]bool{}, jobs: map[string]schedule.Job{}}
	old := scheduleSetup
	scheduleSetup = func() (schedule.Backend, config.Paths, error) {
		p, err := config.ResolvePaths()
		return f, p, err
	}
	t.Cleanup(func() { scheduleSetup = old })

	if code, _, errs := run(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup %d %s", code, errs)
	}
	if code, _, errs := run(t, "schedule", "install"); code != 0 {
		t.Fatalf("install %d %s", code, errs)
	}
	// a host hook (it has the shell's environment) writes the heartbeat a scheduled run looks for
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	st.Heartbeat("claude-code", "0.0.0")
	job, ok := f.jobs[schedule.LabelRun]
	if !ok {
		t.Fatal("run job not installed")
	}

	// The scheduler starts the job without the shell's variables, only with the job's own environment.
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(k, "")
	}
	mode := func() string {
		code, out, errs := run(t, "--json", "run", "--trigger", "schedule")
		if code != 0 {
			t.Fatalf("run %d %s", code, errs)
		}
		var got struct{ Mode string }
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return got.Mode
	}
	if got := mode(); got == "run" {
		t.Fatalf("without the job's environment the default state dir is read, which holds no consent: mode %q", got)
	}
	for _, v := range job.Env {
		t.Setenv(v.Name, v.Value)
	}
	if got := mode(); got != "run" {
		t.Fatalf("with the job's environment the run does not see the consent: mode %q", got)
	}
}

func TestScheduleStatusWarnsWhenJobPathsDiffer(t *testing.T) {
	f := &fakeBackend{installed: map[string]bool{schedule.LabelRun: true}}
	useFake(t, f)
	out := func() string {
		var o, e bytes.Buffer
		if code := Main([]string{"schedule", "status"}, &o, &e); code != 0 {
			t.Fatalf("status %d %s", code, e.String())
		}
		return o.String()
	}
	if got := out(); strings.Contains(got, "schedule install") {
		t.Fatalf("warned without drift: %s", got)
	}
	d := &driftBackend{fakeBackend: f}
	scheduleSetup = func() (schedule.Backend, config.Paths, error) {
		return d, config.Paths{Data: "/d", State: "/s", Config: "/c"}, nil
	}
	if got := out(); !strings.Contains(got, "warning:") || !strings.Contains(got, "schedule install") {
		t.Fatalf("no drift warning: %s", got)
	}
}

// driftBackend reports an installed job whose text lacks every SAPULIDI_ variable.
type driftBackend struct{ *fakeBackend }

func (d *driftBackend) InstalledText(string) (string, bool)  { return "", true }
func (d *driftBackend) EnvFragment(v schedule.EnvVar) string { return v.Name + "=" + v.Value }

func TestScheduleCommandJSON(t *testing.T) {
	f := &fakeBackend{installed: map[string]bool{}}
	useFake(t, f)
	var out, errb bytes.Buffer
	if code := Main([]string{"schedule", "install"}, &out, &errb); code != 0 {
		t.Fatalf("install %d %s", code, errb.String())
	}
	if !f.installed[schedule.LabelRun] || f.installed[schedule.LabelAgent] {
		t.Fatalf("install default must be the run job only: %v", f.installed)
	}
	out.Reset()
	if code := Main([]string{"--json", "schedule", "status"}, &out, &errb); code != 0 {
		t.Fatalf("status %d %s", code, errb.String())
	}
	var got []struct {
		Backend   string
		Label     string
		Installed bool
		Warnings  []string
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, out.String())
	}
	if len(got) != 2 || got[0].Backend != "fake" || got[0].Label != schedule.LabelRun || !got[0].Installed ||
		got[1].Label != schedule.LabelAgent || got[1].Installed || got[0].Warnings == nil {
		t.Fatalf("got %+v", got)
	}
	if bytes.Contains(out.Bytes(), []byte("null")) {
		t.Fatalf("null in %s", out.String())
	}
	// Remove, and usage errors.
	if code := Main([]string{"schedule", "remove"}, &out, &errb); code != 0 || len(f.installed) != 0 {
		t.Fatalf("remove %d %v", code, f.installed)
	}
	if code := Main([]string{"schedule", "bogus"}, &out, &errb); code != 2 {
		t.Fatalf("usage code %d", code)
	}
}
