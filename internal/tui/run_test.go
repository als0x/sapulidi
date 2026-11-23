package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/tui/term"
)

type fakeTerm struct {
	mu       sync.Mutex
	frames   []string
	events   chan term.Event
	enters   int
	restores int
	cols     int
	rows     int
}

func newFakeTerm(cols, rows int) *fakeTerm {
	return &fakeTerm{events: make(chan term.Event, 64), cols: cols, rows: rows}
}

func (f *fakeTerm) Enter() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enters++
	return nil
}

func (f *fakeTerm) Restore() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restores++
}

func (f *fakeTerm) Size() (int, int, error)   { return f.cols, f.rows, nil }
func (f *fakeTerm) Events() <-chan term.Event { return f.events }
func (f *fakeTerm) IsPlain() bool             { return true }

func (f *fakeTerm) Write(lines []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, strings.Join(lines, "\n"))
}

func (f *fakeTerm) screen() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.frames) == 0 {
		return ""
	}
	return f.frames[len(f.frames)-1]
}

func (f *fakeTerm) restored() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restores
}

// waitFor blocks until the last frame contains want.
func (f *fakeTerm) waitFor(t *testing.T, want string) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if strings.Contains(f.screen(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("screen never showed %q; last frame:\n%s", want, f.screen())
}

func (f *fakeTerm) keys(s string) {
	for _, r := range s {
		f.events <- term.Key{Rune: r}
	}
}

func (f *fakeTerm) special(k term.SpecialKey) { f.events <- term.Key{Special: k} }

type fakeDeps struct {
	ft        *fakeTerm
	rows      []Row
	sum       SummaryData
	blockScan bool // Scan waits for its context
	termErr   error

	mu       sync.Mutex
	deletes  [][]string
	excluded []string
	unexcl   []string
	delete   func(paths []string) Result
	exclErr  error
}

func (d *fakeDeps) Terminal() (Terminal, error) {
	if d.termErr != nil {
		return nil, d.termErr
	}
	return d.ft, nil
}

func (d *fakeDeps) Scan(ctx context.Context, progress func(string, int, int)) ([]Row, error) {
	progress("caches", 1, 2)
	if d.blockScan {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	progress("temp", 2, 2)
	return d.rows, nil
}

func (d *fakeDeps) Summary() SummaryData { return d.sum }

func (d *fakeDeps) Delete(_ context.Context, paths []string) Result {
	d.mu.Lock()
	d.deletes = append(d.deletes, append([]string(nil), paths...))
	d.mu.Unlock()
	if d.delete != nil {
		return d.delete(paths)
	}
	return Result{Freed: 1, Removed: paths}
}

func (d *fakeDeps) AddExclusion(p string) error {
	if d.exclErr != nil {
		return d.exclErr
	}
	d.mu.Lock()
	d.excluded = append(d.excluded, p)
	d.mu.Unlock()
	return nil
}

func (d *fakeDeps) RemoveExclusion(p string) error {
	d.mu.Lock()
	d.unexcl = append(d.unexcl, p)
	d.mu.Unlock()
	return nil
}

func (d *fakeDeps) deleted() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([][]string(nil), d.deletes...)
}

func threeRows() []Row {
	return []Row{
		{Path: "/cache/a", Bytes: 300, Source: SrcAuto, Category: "caches"},
		{Path: "/cache/b", Bytes: 200, Source: SrcAuto, Category: "caches"},
		{Path: "/cache/c", Bytes: 100, Source: SrcAuto, Category: "caches"},
	}
}

func newFakes() (*fakeTerm, *fakeDeps) {
	ft := newFakeTerm(100, 30)
	return ft, &fakeDeps{ft: ft, rows: threeRows(), sum: SummaryData{Now: time.Unix(1_700_000_000, 0)}}
}

// start runs Run in the background and returns a channel with its result.
func start(d *fakeDeps) chan error {
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), d) }()
	return done
}

func finish(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

func TestScriptedSessionDeletesExactlyTheSelection(t *testing.T) {
	ft, d := newFakes()
	done := start(d)
	ft.waitFor(t, "3 rows")
	ft.keys(" ") // select /cache/a (largest first)
	ft.special(term.KeyDown)
	ft.keys(" ") // select /cache/b
	ft.special(term.KeyDown)
	ft.keys("x") // exclude /cache/c
	ft.keys("d")
	ft.waitFor(t, "Delete 2 items")
	ft.keys("y")
	ft.waitFor(t, "removed 2 items")
	ft.keys("q") // result -> list
	ft.keys("q") // quit
	if err := finish(t, done); err != nil {
		t.Fatal(err)
	}
	if got, want := d.deleted(), [][]string{{"/cache/a", "/cache/b"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted %v, want %v", got, want)
	}
	if !reflect.DeepEqual(d.excluded, []string{"/cache/c"}) {
		t.Fatalf("excluded %v", d.excluded)
	}
	if ft.enters != 1 || ft.restored() != 1 {
		t.Fatalf("enter %d restore %d", ft.enters, ft.restored())
	}
}

func TestCancelledScanLeavesAnEmptyListAndDeletesNothing(t *testing.T) {
	ft, d := newFakes()
	d.blockScan = true
	done := start(d)
	ft.waitFor(t, "scanning")
	ft.waitFor(t, "caches")
	ft.special(term.KeyEsc)
	ft.waitFor(t, "scan cancelled")
	ft.keys("ad") // select all, delete: nothing to select
	ft.waitFor(t, "nothing selected")
	ft.keys("q")
	if err := finish(t, done); err != nil {
		t.Fatal(err)
	}
	if len(d.deleted()) != 0 {
		t.Fatalf("deleted %v", d.deleted())
	}
	if ft.restored() != 1 {
		t.Fatalf("restores %d", ft.restored())
	}
}

func TestPausedRefusesDelete(t *testing.T) {
	ft, d := newFakes()
	d.sum.Paused = true
	done := start(d)
	ft.waitFor(t, "/cache/a")
	ft.keys(" d")
	ft.waitFor(t, "paused")
	ft.keys("q")
	finish(t, done)
	if len(d.deleted()) != 0 {
		t.Fatalf("deleted %v", d.deleted())
	}
}

func TestPathKeptByAGuardIsReported(t *testing.T) {
	ft, d := newFakes()
	d.delete = func(paths []string) Result {
		return Result{Removed: paths[:1], Kept: []KeptItem{{Path: paths[1], Why: "open"}}}
	}
	done := start(d)
	ft.waitFor(t, "3 rows")
	ft.keys(" ")
	ft.special(term.KeyDown)
	ft.keys(" dy")
	ft.waitFor(t, "Kept by a guard (1)")
	ft.waitFor(t, "/cache/b")
	if !strings.Contains(ft.screen(), "open") || strings.Contains(ft.screen(), "error:") {
		t.Fatalf("screen:\n%s", ft.screen())
	}
	ft.keys("qq")
	finish(t, done)
}

func TestSmallWindowDisablesDelete(t *testing.T) {
	ft, d := newFakes()
	ft.cols, ft.rows = 70, 20
	done := start(d)
	ft.waitFor(t, "make the window larger")
	ft.keys(" d")
	time.Sleep(50 * time.Millisecond)
	ft.events <- term.Resize{Cols: 100, Rows: 30}
	ft.waitFor(t, "3 rows")
	if strings.Contains(ft.screen(), "[x]") || strings.Contains(ft.screen(), "Delete") {
		t.Fatalf("a key worked in a small window:\n%s", ft.screen())
	}
	ft.keys("q")
	finish(t, done)
	if len(d.deleted()) != 0 {
		t.Fatalf("deleted %v", d.deleted())
	}
}

func TestExclusionErrorIsShownAndNothingChanges(t *testing.T) {
	ft, d := newFakes()
	d.exclErr = errors.New("exclude.toml is corrupt")
	done := start(d)
	ft.waitFor(t, "3 rows")
	ft.keys("x")
	ft.waitFor(t, "exclude.toml is corrupt")
	ft.keys("q")
	finish(t, done)
	if len(d.deleted()) != 0 || len(d.excluded) != 0 {
		t.Fatalf("deleted %v excluded %v", d.deleted(), d.excluded)
	}
}

func TestPanicInUpdateRestoresOnce(t *testing.T) {
	ft, d := newFakes()
	old := runUpdate
	runUpdate = func(m Model, msg Msg) (Model, []Effect) {
		if k, ok := msg.(KeyMsg); ok && k.Rune == 'z' {
			panic("boom")
		}
		return Update(m, msg)
	}
	t.Cleanup(func() { runUpdate = old })
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		Run(context.Background(), d)
	}()
	ft.waitFor(t, "3 rows")
	ft.keys("z")
	select {
	case p := <-done:
		if p != "boom" {
			t.Fatalf("recovered %v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no panic")
	}
	if ft.restored() != 1 {
		t.Fatalf("restores %d", ft.restored())
	}
}

func TestContextCancelAndCtrlCRestoreOnce(t *testing.T) {
	ft, d := newFakes()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, d) }()
	ft.waitFor(t, "3 rows")
	cancel()
	if err := finish(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if ft.restored() != 1 {
		t.Fatalf("restores %d", ft.restored())
	}

	ft, d = newFakes()
	done = start(d)
	ft.waitFor(t, "3 rows")
	ft.events <- term.Key{Rune: 'c', Ctrl: true}
	if err := finish(t, done); err != nil {
		t.Fatal(err)
	}
	if ft.restored() != 1 {
		t.Fatalf("restores %d", ft.restored())
	}
}

func TestNoTerminal(t *testing.T) {
	_, d := newFakes()
	d.termErr = ErrNoTerminal
	if err := Run(context.Background(), d); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("err %v", err)
	}
}

func TestRowsOfWholesaleGroupAndKept(t *testing.T) {
	s := engine.Summary{
		WouldRemove: []core.Candidate{
			{Category: "caches", RuleKey: "r@1", Path: "/c/x", Bytes: 10, Source: "optional-off", Reason: "old"},
			{Category: "containers", RuleKey: "i@1", Command: []string{"docker", "image", "prune", "-f"}, Bytes: -1, Reason: "dangling"},
			{Category: "containers", RuleKey: "c@1", Path: "docker:abc1", Bytes: -1},
			{Category: "containers", RuleKey: "c@1", Path: "docker:abc2", Bytes: 5},
			{Category: "containers", RuleKey: "c@1", Path: "podman:def3", Bytes: -1},
		},
		Kept: []core.Kept{
			{Category: "caches", Path: "/c/y", Why: "open"},
			{Category: "caches", Path: "/c/z", Why: "excluded"},
			{Category: "worktrees", Why: "error"}, // no path: not a row
		},
	}
	rows := rowsOf(s)
	by := map[string]Row{}
	for _, r := range rows {
		by[r.Path] = r
	}
	if len(rows) != 6 {
		t.Fatalf("%d rows: %+v", len(rows), rows)
	}
	if r := by["/c/x"]; r.Source != SrcOptionalOff || !r.selectable() || r.Bytes != 10 {
		t.Fatalf("%+v", r)
	}
	w := by["docker image prune -f"]
	if w.selectable() || w.Block != WholesaleReason("docker image prune -f") {
		t.Fatalf("wholesale row %+v", w)
	}
	g := by["docker: stopped containers"]
	if !reflect.DeepEqual(g.Members, []string{"docker:abc1", "docker:abc2"}) || g.Bytes != 5 || !g.selectable() {
		t.Fatalf("group %+v", g)
	}
	if p := by["podman: stopped containers"]; len(p.Members) != 1 || p.Bytes != -1 {
		t.Fatalf("podman group %+v", p)
	}
	if r := by["/c/y"]; r.Source != SrcKept || r.selectable() {
		t.Fatalf("%+v", r)
	}
	if r := by["/c/z"]; r.Source != SrcExcluded || r.selectable() {
		t.Fatalf("%+v", r)
	}
}

func TestEngineDepsDeleteUsesExplicitPathsAndTuiTrigger(t *testing.T) {
	var got engine.Options
	e := &EngineDeps{
		Build: func() (*core.Env, error) { return &core.Env{}, nil },
		Run: func(env *core.Env, opt engine.Options) (engine.Summary, error) {
			got = opt
			return engine.Summary{Mode: "run", FreedBytes: 7,
				Removed: []core.Candidate{{Path: "/a"}},
				Kept:    []core.Kept{{Path: "/b", Why: "open"}},
				Failed:  []core.Kept{{Path: "/c", Why: "error"}}}, nil
		},
	}
	res := e.Delete(context.Background(), []string{"/a", "/b", "/c"})
	if !reflect.DeepEqual(got.Explicit, []string{"/a", "/b", "/c"}) || got.Trigger != "tui" || len(got.Paths) != 0 {
		t.Fatalf("options %+v", got)
	}
	want := Result{Freed: 7, Removed: []string{"/a"}, Kept: []KeptItem{{"/b", "open"}, {"/c", "failed"}}}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("result %+v", res)
	}
}

func TestEngineDepsDeleteRefusals(t *testing.T) {
	for mode, want := range map[string]string{"paused": "paused", "busy": "another run", "error": "nothing was deleted"} {
		e := &EngineDeps{
			Build: func() (*core.Env, error) { return &core.Env{}, nil },
			Run:   func(*core.Env, engine.Options) (engine.Summary, error) { return engine.Summary{Mode: mode}, nil },
		}
		if r := e.Delete(context.Background(), []string{"/a"}); !strings.Contains(r.Err, want) {
			t.Errorf("%s: %+v", mode, r)
		}
	}
	e := &EngineDeps{Build: func() (*core.Env, error) { return nil, errors.New("exclude.toml is corrupt") }}
	if r := e.Delete(context.Background(), []string{"/a"}); r.Err != "exclude.toml is corrupt" {
		t.Fatalf("%+v", r)
	}
	if _, err := e.Scan(context.Background(), nil); err == nil {
		t.Fatal("scan must fail when the env cannot be built")
	}
}

func TestSummaryFacts(t *testing.T) {
	e := &EngineDeps{
		now: time.Unix(1_700_000_000, 0), freeNow: 42,
		Facts: func() Facts { return Facts{Agents: 2, Interval: 30 * time.Minute, NextIn: "12m", Paused: true} },
	}
	s := e.Summary()
	if s.Agents != 2 || s.Interval != 30*time.Minute || !s.Paused || s.FreeNow != 42 || !s.Next.Equal(e.now.Add(12*time.Minute)) {
		t.Fatalf("%+v", s)
	}
	e.Facts = func() Facts { return Facts{NextIn: "12m"} } // no job: no next run
	if s := e.Summary(); !s.Next.IsZero() || s.Interval != 0 {
		t.Fatalf("%+v", s)
	}
}
