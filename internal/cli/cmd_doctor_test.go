package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/schedule"
	"github.com/als0x/sapulidi/internal/testutil"
)

func TestDoctorJSONShape(t *testing.T) {
	home := testutil.TempHome(t)
	be := &fakeBackend{installed: map[string]bool{schedule.LabelRun: true}}
	oldSched, oldEnv := scheduleSetup, doctorEnv
	scheduleSetup = func() (schedule.Backend, config.Paths, error) {
		return be, config.Paths{Config: filepath.Join(home, "c", "config.toml"),
			State: filepath.Join(home, "s"), Data: filepath.Join(home, "d")}, nil
	}
	doctorEnv = func() (core.Platform, core.Runner) {
		run := &testutil.FakeRunner{Paths: map[string]string{"claude": "/bin/claude", "osascript": "/bin/osascript",
			"notify-send": "/bin/notify-send", "lsof": "/bin/lsof"}}
		run.On([]string{"git", "--version"}, "git version 2.45.0\n", nil)
		return &testutil.FakePlatform{OSName: "darwin", Free: 100 << 30}, run
	}
	t.Cleanup(func() { scheduleSetup, doctorEnv = oldSched, oldEnv })

	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "doctor"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	var doc struct {
		Checks []struct{ Name, Status, Detail string }
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, out.String())
	}
	got := map[string]string{}
	for _, c := range doc.Checks {
		if c.Status != "ok" && c.Status != "warn" {
			t.Fatalf("status %q of %s", c.Status, c.Name)
		}
		if c.Detail == "" {
			t.Fatalf("%s has no detail", c.Name)
		}
		got[c.Name] = c.Status
	}
	for _, name := range []string{"os", "arch", "wsl", "version", "config", "state", "data", "git", "open-files",
		"notifier", "scheduler", "agent", "disk"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("no %q check in %s", name, out.String())
		}
	}
	if got["git"] != "ok" || got["scheduler"] != "warn" || got["agent"] != "ok" || got["disk"] != "ok" {
		t.Fatalf("statuses %v (scheduler is warn: the agent job is not installed)", got)
	}
	if bytes.Contains(out.Bytes(), []byte("null")) {
		t.Fatalf("null in %s", out.String())
	}
}

func TestDoctorShowsTheScheduleInterval(t *testing.T) {
	be := &fakeBackend{installed: map[string]bool{schedule.LabelRun: true, schedule.LabelAgent: true}}
	p := useFake(t, be)
	_ = config.Apply(p, config.Change{Key: "schedule.interval", Value: "2h"})
	oldEnv := doctorEnv
	doctorEnv = func() (core.Platform, core.Runner) {
		run := &testutil.FakeRunner{Paths: map[string]string{}}
		run.On([]string{"git", "--version"}, "git version 2.45.0\n", nil)
		return &testutil.FakePlatform{OSName: "linux", Free: 100 << 30}, run
	}
	t.Cleanup(func() { doctorEnv = oldEnv })
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "doctor"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	var doc struct {
		Checks []struct{ Name, Detail string }
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Checks {
		if c.Name == "scheduler" {
			if !strings.Contains(c.Detail, schedule.LabelRun+": installed, every 2h") ||
				!strings.Contains(c.Detail, schedule.LabelAgent+": installed, every 7d") {
				t.Fatalf("detail %q", c.Detail)
			}
			return
		}
	}
	t.Fatal("no scheduler check")
}

func TestDoctorWarnsWhenToolsAreMissing(t *testing.T) {
	home := testutil.TempHome(t)
	oldSched, oldEnv := scheduleSetup, doctorEnv
	scheduleSetup = func() (schedule.Backend, config.Paths, error) {
		return &fakeBackend{installed: map[string]bool{}}, config.Paths{Config: filepath.Join(home, "c", "config.toml"),
			State: filepath.Join(home, "s"), Data: filepath.Join(home, "d")}, nil
	}
	doctorEnv = func() (core.Platform, core.Runner) {
		run := &testutil.FakeRunner{Paths: map[string]string{}}
		run.On([]string{"git", "--version"}, "", errNotFound)
		return &testutil.FakePlatform{OSName: "linux", Free: 1 << 30}, run
	}
	t.Cleanup(func() { scheduleSetup, doctorEnv = oldSched, oldEnv })
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "doctor"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	var doc struct {
		Checks []struct{ Name, Status string }
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Checks {
		switch c.Name {
		case "git", "agent", "disk", "notifier", "scheduler":
			if c.Status != "warn" {
				t.Fatalf("%s should warn: %s", c.Name, out.String())
			}
		}
	}
}
