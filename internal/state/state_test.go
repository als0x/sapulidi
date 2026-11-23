package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/testutil"
)

func open(t *testing.T) *Store {
	t.Helper()
	testutil.TempHome(t)
	p, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func plantLock(t *testing.T, s *Store, pid int, start time.Time) string {
	t.Helper()
	dir := s.path("lock")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	must := func(name, v string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must("pid", strconv.Itoa(pid))
	must("start", strconv.FormatInt(start.UnixNano(), 10))
	must("token", "foreign")
	return dir
}

func TestOpenCreatesPrivateDirAndRefusesSymlink(t *testing.T) {
	s := open(t)
	fi, err := os.Stat(s.dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("state dir: %v %v", fi, err)
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(config.Paths{State: link}); err == nil {
		t.Fatal("symlinked state dir must be refused")
	}
	// a symlinked sub directory too
	st := filepath.Join(base, "st")
	if err := os.Mkdir(st, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(st, "hosts")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(config.Paths{State: st}); err == nil {
		t.Fatal("symlinked hosts dir must be refused")
	}
	if _, err := Open(config.Paths{State: "rel"}); err == nil {
		t.Fatal("relative state dir must be refused")
	}
}

func TestLockBusyForLiveOwner(t *testing.T) {
	s := open(t)
	release, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Lock: %v", err)
	}
	release()
	r2, err := s.Lock()
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	r2()
	if _, err := os.Lstat(s.path("lock")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("lock dir must be gone after release")
	}
}

func TestLockTakesOverDeadOwner(t *testing.T) {
	s := open(t)
	plantLock(t, s, 0x3ffffff0, time.Now()) // no such process
	release, err := s.Lock()
	if err != nil {
		t.Fatalf("dead owner must be taken over: %v", err)
	}
	release()
}

func TestLockTakesOverRecycledPid(t *testing.T) {
	s := open(t)
	start, err := execx.ProcessStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	plantLock(t, s, os.Getpid(), start.Add(-48*time.Hour)) // same pid, different start time
	release, err := s.Lock()
	if err != nil {
		t.Fatalf("recycled pid must be taken over: %v", err)
	}
	release()
}

func TestLockStaleOwnerless(t *testing.T) {
	s := open(t)
	dir := s.path("lock")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(); !errors.Is(err, ErrBusy) {
		t.Fatalf("young owner-less lock must block: %v", err)
	}
	old := time.Now().Add(-61 * time.Minute)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := s.Lock()
	if err != nil {
		t.Fatalf("old owner-less lock must be taken over: %v", err)
	}
	release()
}

func TestLockOnlyOwnerReleases(t *testing.T) {
	s := open(t)
	release, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	// someone else takes the lock over (our owner looked dead to them)
	if err := os.RemoveAll(s.path("lock")); err != nil {
		t.Fatal(err)
	}
	other := plantLock(t, s, os.Getpid(), time.Now())
	release()
	if _, err := os.Stat(filepath.Join(other, "token")); err != nil {
		t.Fatal("a release that is not the owner removed the lock")
	}
	release() // twice is harmless
}

func TestLockOneWinnerAmongRacers(t *testing.T) {
	s := open(t)
	plantLock(t, s, 0x3ffffff0, time.Now())
	const n = 8
	res := make(chan error, n)
	rel := make(chan func(), n)
	for i := 0; i < n; i++ {
		go func() {
			r, err := s.Lock()
			if err == nil {
				rel <- r
			}
			res <- err
		}()
	}
	won := 0
	for i := 0; i < n; i++ {
		if err := <-res; err == nil {
			won++
		} else if !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatalf("%d racers won the lock", won)
	}
	(<-rel)()
}

func TestPauseResume(t *testing.T) {
	s := open(t)
	if s.Paused() {
		t.Fatal("not paused at start")
	}
	s.Pause()
	if !s.Paused() {
		t.Fatal("Pause")
	}
	s.Resume()
	if s.Paused() {
		t.Fatal("Resume")
	}
}

func TestConsentRoundTrip(t *testing.T) {
	s := open(t)
	if c, err := s.Consent(); c != nil || err != nil {
		t.Fatalf("no consent yet: %v %v", c, err)
	}
	at := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	if err := s.SaveConsent(&Consent{At: at, Rules: []string{"go/go-build@1"}}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Consent()
	if err != nil || !c.At.Equal(at) || len(c.Rules) != 1 || c.Rules[0] != "go/go-build@1" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestLogRotatesAt1MB(t *testing.T) {
	s := open(t)
	l := s.Logger()
	l.Event("removed", map[string]any{"path": "/x", "bytes": 5})
	b, err := os.ReadFile(s.path("log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var ev map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(b), &ev); err != nil || ev["kind"] != "removed" || ev["t"] == nil || ev["path"] != "/x" {
		t.Fatalf("event: %s %v", b, err)
	}
	pad := strings.Repeat("x", 10_000)
	for i := 0; i < 110; i++ {
		l.Event("pad", map[string]any{"pad": pad})
	}
	old, err := os.Stat(s.path("log.jsonl.1"))
	if err != nil || old.Size() < logMaxBytes {
		t.Fatalf("rotated file: %v %v", old, err)
	}
	cur, err := os.Stat(s.path("log.jsonl"))
	if err != nil || cur.Size() >= logMaxBytes {
		t.Fatalf("current log must restart small: %v %v", cur, err)
	}
	if _, err := os.Stat(s.path("log.jsonl.2")); err == nil {
		t.Fatal("only one old file is kept")
	}
}

func TestFactNotifiesOnceUntilChanged(t *testing.T) {
	s := open(t)
	if !s.Fact("worktree:a", "kept: unsaved work", false) {
		t.Fatal("new fact must notify")
	}
	if s.Fact("worktree:a", "kept: unsaved work", false) {
		t.Fatal("same fact must not notify again")
	}
	if !s.Fact("worktree:a", "kept: 2 worktrees", false) {
		t.Fatal("changed text must notify")
	}
	if !s.Fact("worktree:a", "kept: 2 worktrees", true) {
		t.Fatal("caller-reported change must notify")
	}
	if !s.Fact("worktree:b", "other", false) {
		t.Fatal("another key is independent")
	}
	// survives a reopen
	s2, err := Open(config.Paths{State: s.dir})
	if err != nil {
		t.Fatal(err)
	}
	if s2.Fact("worktree:b", "other", false) {
		t.Fatal("facts must persist")
	}
	b, _ := os.ReadFile(s.path("log.jsonl"))
	if n := strings.Count(string(b), `"kind":"fact"`); n != 4 {
		t.Fatalf("a fact is logged when new or changed only; got %d lines", n)
	}
}

func TestPendingFactsAfterFailedNotify(t *testing.T) {
	s := open(t)
	if got := s.PendingFacts(); len(got) != 0 {
		t.Fatalf("nothing pending: %v", got)
	}
	s.Fact("b", "text b", false)
	s.Fact("a", "text a", false)
	s.Fact("c", "delivered", false)
	s.MarkUndelivered("b")
	s.MarkUndelivered("a")
	s.MarkUndelivered("nope")
	got := s.PendingFacts()
	if len(got) != 2 || got[0] != "text a" || got[1] != "text b" {
		t.Fatalf("pending: %v", got)
	}
	if got := s.PendingFacts(); len(got) != 0 {
		t.Fatalf("returned once, then cleared: %v", got)
	}
	// the fact itself is still known: no new notification for the same text
	if s.Fact("a", "text a", false) {
		t.Fatal("cleared flag must not make the fact new")
	}
}

func TestGrowthWindow(t *testing.T) {
	s := open(t)
	base := time.Now()
	at := func(d time.Duration) { s.now = func() time.Time { return base.Add(d) } }
	day := 24 * time.Hour

	at(-40 * day) // older than 5 weeks: dropped
	s.AddGrowthSample(map[string]int64{"/d": 1})
	at(-10 * day) // outside the 7 d window
	s.AddGrowthSample(map[string]int64{"/d": 100})
	at(-6 * day)
	s.AddGrowthSample(map[string]int64{"/d": 1000, "/e": 7})
	at(-1 * day)
	s.AddGrowthSample(map[string]int64{"/d": 1500})
	at(0)
	if got := s.Growth("/d", 7*day); got != 500 {
		t.Fatalf("growth over 7d: %d", got)
	}
	if got := s.Growth("/d", 20*day); got != 1400 {
		t.Fatalf("growth over 20d: %d", got)
	}
	if got := s.Growth("/e", 7*day); got != 0 {
		t.Fatalf("one sample: %d", got)
	}
	if got := s.Growth("/unknown", 7*day); got != 0 {
		t.Fatalf("unknown dir: %d", got)
	}
	if got := s.Growth("/d", 12*time.Hour); got != 0 {
		t.Fatalf("no sample in window: %d", got)
	}
	if n := len(s.readSamples()); n != 3 {
		// 4 samples written, the 40-day one is dropped by the later writes
		t.Fatalf("samples kept: %d", n)
	}
}

func TestHeartbeatRoundTrip(t *testing.T) {
	s := open(t)
	when := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	s.now = func() time.Time { return when }
	s.Heartbeat("claude-code", "0.1.0")
	s.Heartbeat("../evil", "x")
	s.Heartbeat("", "x")
	hb := s.Heartbeats()
	if len(hb) != 1 {
		t.Fatalf("heartbeats: %v", hb)
	}
	got := hb["claude-code"]
	if got.Version != "0.1.0" || !got.At.Equal(when) {
		t.Fatalf("%+v want version 0.1.0 at %v", got, when)
	}
	s.Heartbeat("claude-code", "0.2.0")
	if s.Heartbeats()["claude-code"].Version != "0.2.0" {
		t.Fatal("heartbeat must be overwritten")
	}
}

func TestRunRecordAndLastRun(t *testing.T) {
	s := open(t)
	if _, ok := s.LastRun(); ok {
		t.Fatal("no run yet")
	}
	when := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return when }
	s.RunRecord(map[string]int{"removed": 3})
	got, ok := s.LastRun()
	if !ok || !got.Equal(when) {
		t.Fatalf("%v %v", got, ok)
	}
	b, _ := os.ReadFile(s.path("log.jsonl"))
	if !strings.Contains(string(b), `"kind":"run"`) || !strings.Contains(string(b), `"removed":3`) {
		t.Fatalf("run summary must be logged: %s", b)
	}
	if fi, err := os.Stat(s.ProposalsDir()); err != nil || !fi.IsDir() {
		t.Fatal("proposals dir")
	}
}

func TestFilesAre0600(t *testing.T) {
	s := open(t)
	rel, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	s.Pause()
	_ = s.SaveConsent(&Consent{At: time.Now()})
	s.Logger().Event("x", nil)
	s.Fact("k", "v", false)
	s.AddGrowthSample(map[string]int64{"/d": 1})
	s.Heartbeat("h", "1")
	s.RunRecord("done")
	err = filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == s.dir {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v want %v", p, fi.Mode().Perm(), want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rel()
}

func TestLockStartSkewTolerated(t *testing.T) {
	s := open(t)
	start, err := execx.ProcessStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	plantLock(t, s, os.Getpid(), start.Add(-time.Second))
	if _, err := s.Lock(); !errors.Is(err, ErrBusy) {
		t.Fatalf("1 s skew on a live pid must stay busy: %v", err)
	}
	if err := os.RemoveAll(s.path("lock")); err != nil {
		t.Fatal(err)
	}
	plantLock(t, s, os.Getpid(), start.Add(-10*time.Second))
	release, err := s.Lock()
	if err != nil {
		t.Fatalf("10 s off must be stale: %v", err)
	}
	release()
}

func TestLockLiveLockReplacesStaleBetweenCheckAndRename(t *testing.T) {
	s := open(t)
	plantLock(t, s, 0x3ffffff0, time.Now())
	start, err := execx.ProcessStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	s.afterStaleCheck = func() {
		s.afterStaleCheck = nil
		if err := os.RemoveAll(s.path("lock")); err != nil {
			t.Error(err)
		}
		plantLock(t, s, os.Getpid(), start)
		_ = os.WriteFile(s.path("lock", "token"), []byte("live"), 0o600)
	}
	if _, err := s.Lock(); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy: %v", err)
	}
	tok, err := os.ReadFile(s.path("lock", "token"))
	if err != nil || string(tok) != "live" {
		t.Fatalf("the live owner must still hold the lock: %q %v", tok, err)
	}
	if m, _ := filepath.Glob(s.path("lock.stale-*")); len(m) != 0 {
		t.Fatalf("leftover trash: %v", m)
	}
}

func TestLockPutBackFailureKeepsTrash(t *testing.T) {
	s := open(t)
	plantLock(t, s, 0x3ffffff0, time.Now())
	start, _ := execx.ProcessStart(os.Getpid())
	s.afterStaleCheck = func() {
		s.afterStaleCheck = nil
		_ = os.RemoveAll(s.path("lock"))
		plantLock(t, s, os.Getpid(), start)
		_ = os.WriteFile(s.path("lock", "token"), []byte("live"), 0o600)
		// by the time we put it back, yet another lock sits there
		s.afterRename = func() {
			s.afterRename = nil
			if err := os.Mkdir(s.path("lock"), 0o700); err != nil {
				t.Error(err)
			}
			_ = os.WriteFile(s.path("lock", "pid"), []byte("1"), 0o600)
		}
	}
	if _, err := s.Lock(); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy: %v", err)
	}
	m, _ := filepath.Glob(s.path("lock.stale-*"))
	if len(m) != 1 {
		t.Fatalf("the moved live lock must be kept: %v", m)
	}
	if tok, _ := os.ReadFile(filepath.Join(m[0], "token")); string(tok) != "live" {
		t.Fatal("kept trash must be the live lock")
	}
}

func TestLockSweepsOldLeftovers(t *testing.T) {
	s := open(t)
	old := time.Now().Add(-2 * time.Hour)
	for _, n := range []string{".lock-tmp-abc", "lock.stale-abc"} {
		d := s.path(n)
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}
	young := s.path("lock.stale-young")
	if err := os.Mkdir(young, 0o700); err != nil {
		t.Fatal(err)
	}
	release, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	release()
	for _, n := range []string{".lock-tmp-abc", "lock.stale-abc"} {
		if _, err := os.Stat(s.path(n)); err == nil {
			t.Errorf("%s must be swept", n)
		}
	}
	if _, err := os.Stat(young); err != nil {
		t.Error("a young leftover must stay")
	}
}

func TestLockReleaseConcurrentIsSafe(t *testing.T) {
	s := open(t)
	release, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() { release(); done <- struct{}{} }()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestCorruptFactsMovedAside(t *testing.T) {
	s := open(t)
	if err := os.WriteFile(s.path("facts.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !s.Fact("k", "v", false) {
		t.Fatal("fact after corruption is new")
	}
	b, err := os.ReadFile(s.path("facts.json.bad"))
	if err != nil || string(b) != "{not json" {
		t.Fatalf("bad file: %q %v", b, err)
	}
	if s.Fact("k", "v", false) {
		t.Fatal("new facts.json must be valid")
	}
	l, _ := os.ReadFile(s.path("log.jsonl"))
	if !strings.Contains(string(l), "facts-corrupt") {
		t.Fatalf("not logged: %s", l)
	}
}
