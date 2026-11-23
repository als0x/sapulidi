package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/schedule"
	"github.com/als0x/sapulidi/internal/testutil"
)

type fakeBackend struct {
	installed map[string]bool
	warn      []string
	jobs      map[string]schedule.Job // the last job installed per label
}

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) Install(j schedule.Job) error {
	f.installed[j.Label] = true
	if f.jobs != nil {
		f.jobs[j.Label] = j
	}
	return nil
}
func (f *fakeBackend) Remove(l string) error { delete(f.installed, l); return nil }
func (f *fakeBackend) Status(l string) (schedule.Status, error) {
	return schedule.Status{Installed: f.installed[l], Warnings: f.warn}, nil
}

func useFake(t *testing.T, f *fakeBackend) {
	home := testutil.TempHome(t)
	old := scheduleSetup
	scheduleSetup = func() (schedule.Backend, config.Paths, error) {
		return f, config.Paths{Data: filepath.Join(home, "d"), State: filepath.Join(home, "s")}, nil
	}
	t.Cleanup(func() { scheduleSetup = old })
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
