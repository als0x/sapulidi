package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/procs"
	"github.com/als0x/sapulidi/internal/testutil"
)

const procsRoot = "/work/proj"

// useProcs installs a pretend machine with one orphaned dev server (pid 700) and no real terminal.
func useProcs(t *testing.T) *testutil.FakeRunner {
	return useProcsWith(t, "node /work/proj/node_modules/.bin/vite "+strings.Repeat("x", 120), "/web")
}

// useProcsWith is useProcs with the command and the directory suffix of process 700 chosen by the test.
func useProcsWith(t *testing.T, command, cwdSuffix string) *testutil.FakeRunner {
	t.Helper()
	run := &testutil.FakeRunner{Paths: map[string]string{"lsof": "/usr/sbin/lsof"}}
	run.On([]string{"ps", "-o", "pid=,ppid=,tty=,command="}, " 700     1 ??       "+command+"\n", nil)
	run.On([]string{"lsof", "-nP", "-a", "-d", "cwd"}, "p700\nn"+procsRoot+cwdSuffix+"\n", nil)
	run.On([]string{"lsof", "-nP", "-a", "-iTCP"}, "p700\nn*:5173\n", nil)
	oldBuild, oldStart, oldTTY := BuildEnv, procs.StartOf, confirm.OpenTTY
	BuildEnv = func() (*core.Env, error) {
		return &core.Env{Roots: []core.Root{{Path: procsRoot, Source: "config"}}, Run: run, Ctx: context.Background(),
			Platform: &testutil.FakePlatform{}, Settings: &testutil.FakeSettings{}}, nil
	}
	procs.StartOf = func(int) (time.Time, error) { return time.Now().Add(-90 * time.Minute), nil }
	confirm.OpenTTY = func() (io.ReadWriteCloser, error) { return nil, errors.New("device not configured") }
	t.Cleanup(func() { BuildEnv, procs.StartOf, confirm.OpenTTY = oldBuild, oldStart, oldTTY })
	return run
}

func TestProcsListSanitizesUntrustedText(t *testing.T) {
	useProcsWith(t, "node /work/proj/node_modules/.bin/vite --x\x1b[2K\rFAKE\x08\x00", "/we\x1bb")
	var out, errb bytes.Buffer
	if code := Main([]string{"procs"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	s := out.String()
	for _, bad := range []string{"\x1b", "\r", "\x08", "\x00"} {
		if strings.Contains(s, bad) {
			t.Errorf("raw control %q in output %q", bad, s)
		}
	}
	if !strings.Contains(s, "vite --x?[2K?FAKE??") || !strings.Contains(s, procsRoot+"/we?b") {
		t.Errorf("sanitized text missing: %q", s)
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("a\x1b[2Kb\rc\td\u0085e\x7f"); got != "a?[2Kb?c?d?e?" {
		t.Errorf("got %q", got)
	}
	if got := sanitize("plain ünïcode"); got != "plain ünïcode" {
		t.Errorf("got %q", got)
	}
}

func TestProcsListWarnsWhenPortsUnchecked(t *testing.T) {
	run := useProcs(t)
	run.On([]string{"lsof", "-nP", "-a", "-iTCP"}, "", errors.New("lsof: timeout"))
	var out, errb bytes.Buffer
	if code := Main([]string{"procs"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "could not check listening ports") {
		t.Errorf("no warning in text:\n%s", out.String())
	}
	out.Reset()
	Main([]string{"--json", "procs"}, &out, &errb)
	var doc struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Warnings) != 1 {
		t.Fatalf("json warnings: %v %s", err, out.String())
	}
}

func TestProcsListText(t *testing.T) {
	useProcs(t)
	var out, errb bytes.Buffer
	if code := Main([]string{"procs"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	s := out.String()
	for _, want := range []string{"700", "1h", procsRoot + "/web", "5173", "node /work/proj/node_modules/.bin/vite"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, strings.Repeat("x", 60)) {
		t.Errorf("command not trimmed to 80 chars:\n%s", s)
	}
}

func TestProcsListJSON(t *testing.T) {
	useProcs(t)
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "procs"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	var doc struct {
		Processes []struct {
			PID   int
			Ports []int
		} `json:"processes"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, out.String())
	}
	if len(doc.Processes) != 1 || doc.Processes[0].PID != 700 || len(doc.Processes[0].Ports) != 1 {
		t.Fatalf("got %+v", doc)
	}
}

func TestStopWithoutTerminalPrintsCommand(t *testing.T) {
	useProcs(t)
	var out, errb bytes.Buffer
	code := Main([]string{"procs", "stop", "700"}, &out, &errb)
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "run this in your own terminal: sapulidi procs stop 700") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestStopUsageErrors(t *testing.T) {
	useProcs(t)
	for _, args := range [][]string{{"procs", "stop"}, {"procs", "stop", "abc"}, {"procs", "stop", "1", "2"}, {"procs", "kill"}} {
		var out, errb bytes.Buffer
		if code := Main(args, &out, &errb); code != 2 {
			t.Errorf("%v: code %d", args, code)
		}
	}
}
