package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

// setHeartbeat writes a heartbeat for host whose mtime is age old.
func (f *fix) setHeartbeat(host string, age time.Duration) {
	f.t.Helper()
	f.st.Heartbeat(host, "0.1.0")
	p := filepath.Join(f.home, ".local", "state", "sapulidi", "hosts", host)
	testutil.Touch(f.t, p, age)
}

type factRec map[string]string

func (r factRec) Fact(k, v string) { r[k] = v }

func TestUninstalledModeAfter30DaysWithoutHeartbeat(t *testing.T) {
	f := newFix(t)
	f.setHeartbeat("claude-code", 31*24*time.Hour)
	facts := factRec{}
	f.env.Facts = facts // Run swaps in its own sink, so the text is checked on a direct call
	if got := heartbeatHook(f.env, f.st, Options{Trigger: "schedule"}); got != "uninstalled" {
		t.Fatalf("got %q", got)
	}
	if facts["uninstalled"] != "sapulidi seems uninstalled; run sapulidi uninstall or open a session" {
		t.Fatalf("facts %v", facts)
	}
	SetBeforeRun(heartbeatHook)
	p := f.mk("a")
	s := f.do(Options{Trigger: "schedule"}, f.cat("temp", core.Auto, f.cand(p)))
	if s.Mode != "uninstalled" || !exists(p) || !hasPath(s.WouldRemove, p) {
		t.Fatalf("%+v", s)
	}
}

func TestUninstalledModeWithNoHeartbeatAtAll(t *testing.T) {
	f := newFix(t)
	if got := heartbeatHook(f.env, f.st, Options{Trigger: "schedule"}); got != "uninstalled" {
		t.Fatalf("got %q", got)
	}
}

func TestHeartbeatKeepsRunning(t *testing.T) {
	f := newFix(t)
	f.setHeartbeat("old-host", 90*24*time.Hour)
	f.setHeartbeat("claude-code", 2*24*time.Hour) // one fresh host is enough
	SetBeforeRun(heartbeatHook)
	p := f.mk("a")
	s := f.do(Options{Trigger: "schedule"}, f.cat("temp", core.Auto, f.cand(p)))
	if s.Mode != "run" || exists(p) {
		t.Fatalf("%+v", s)
	}
}

func TestFutureHeartbeatDoesNotKeepScheduleAlive(t *testing.T) {
	f := newFix(t)
	f.setHeartbeat("claude-code", -365*24*time.Hour) // mtime a year ahead (a 2099 file would do the same)
	if got := heartbeatHook(f.env, f.st, Options{Trigger: "schedule"}); got != "uninstalled" {
		t.Fatalf("future beat kept the schedule alive: %q", got)
	}
	f.setHeartbeat("claude-code", -4*time.Minute) // a small clock skew is allowed
	if got := heartbeatHook(f.env, f.st, Options{Trigger: "schedule", DryRun: true}); got != "" {
		t.Fatalf("4 minutes of skew must count as fresh: %q", got)
	}
	f.setHeartbeat("claude-code", -10*time.Minute)
	if got := heartbeatHook(f.env, f.st, Options{Trigger: "schedule"}); got != "uninstalled" {
		t.Fatalf("10 minutes ahead is too far: %q", got)
	}
}

func TestHookRefusesGCOutsideSapulidiDirs(t *testing.T) {
	f := newFix(t)
	f.setHeartbeat("claude-code", time.Hour)
	docs := filepath.Join(f.home, "Documents")
	old := filepath.Join(docs, "bin", "0.0.1")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, old, 90*24*time.Hour)
	t.Setenv("SAPULIDI_DATA", docs)
	if got := heartbeatHook(f.env, f.st, Options{Trigger: "schedule"}); got != "" || !exists(old) {
		t.Fatalf("got %q; GC must refuse a data directory not named sapulidi", got)
	}
}

func TestHeartbeatOnlyJudgesScheduledRuns(t *testing.T) {
	f := newFix(t) // no heartbeat at all
	for _, trig := range []string{"", "session", "manual"} {
		if got := heartbeatHook(f.env, f.st, Options{Trigger: trig, DryRun: true}); got != "" {
			t.Fatalf("trigger %q: got %q", trig, got)
		}
	}
}

// A run for Explicit paths (the terminal UI) must not garbage-collect old binaries: the user confirmed a list, and
// the binaries were not on it.
func TestExplicitRunDoesNotGCOldBinaries(t *testing.T) {
	f := newFix(t)
	f.setHeartbeat("claude-code", time.Hour)
	bin := filepath.Join(f.home, ".local", "share", "sapulidi", "bin", "0.0.1")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, bin, 90*24*time.Hour)
	SetBeforeRun(heartbeatHook)
	p := f.mk("a")
	s := f.do(Options{Explicit: []string{p}, Trigger: "tui"}, f.cat("temp", core.Auto, f.cand(p)))
	if s.Mode != "run" || exists(p) {
		t.Fatalf("the picked path must be removed: %+v", s)
	}
	if !exists(bin) {
		t.Fatal("an Explicit run removed an old binary directory")
	}
}

func TestHookGCsOldBinariesOncePerDay(t *testing.T) {
	f := newFix(t)
	f.setHeartbeat("claude-code", time.Hour)
	bin := filepath.Join(f.home, ".local", "share", "sapulidi", "bin", "0.0.1")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, bin, 90*24*time.Hour)
	if got := heartbeatHook(f.env, f.st, Options{DryRun: true}); got != "" || !exists(bin) {
		t.Fatalf("a dry run must not remove anything: %q", got)
	}
	if got := heartbeatHook(f.env, f.st, Options{Trigger: "schedule"}); got != "" || exists(bin) {
		t.Fatalf("got %q, bin exists %v", got, exists(bin))
	}
}
