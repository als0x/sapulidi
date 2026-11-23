package procs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/testutil"
)

const root = "/work/proj"

var psPrefix = []string{"ps", "-o", "pid=,ppid=,tty=,command="}
var cwdPrefix = []string{"lsof", "-nP", "-a", "-d", "cwd", "-Fpn"}
var reReadPrefix = []string{"ps", "-o", "command=", "-ww", "-p", "600"}
var portPrefix = []string{"lsof", "-nP", "-a", "-iTCP", "-sTCP:LISTEN", "-Fpn"}

type fixture struct {
	env *core.Env
	run *testutil.FakeRunner
}

// newFixture scripts ps, the cwd and the listening ports of a pretend machine.
func newFixture(t *testing.T, ps string, cwds map[int]string, ports map[int][]string) *fixture {
	t.Helper()
	run := &testutil.FakeRunner{Paths: map[string]string{"lsof": "/usr/sbin/lsof"}}
	run.On(psPrefix, ps, nil)
	var cwd, port strings.Builder
	for pid, p := range cwds {
		fmt.Fprintf(&cwd, "p%d\nn%s\n", pid, p)
	}
	for pid, ps := range ports {
		fmt.Fprintf(&port, "p%d\n", pid)
		for _, p := range ps {
			fmt.Fprintf(&port, "n%s\n", p)
		}
	}
	run.On(cwdPrefix, cwd.String(), nil)
	run.On(portPrefix, port.String(), nil)
	old := StartOf
	StartOf = func(int) (time.Time, error) { return time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC), nil }
	t.Cleanup(func() { StartOf = old })
	env := &core.Env{
		Now: time.Now(), Roots: []core.Root{{Path: root, Source: "config"}},
		Platform: &testutil.FakePlatform{}, Run: run, Settings: &testutil.FakeSettings{}, Log: &testutil.MemLogger{},
		Ctx: context.Background(),
	}
	return &fixture{env: env, run: run}
}

func pids(ps []Proc) []int {
	var out []int
	for _, p := range ps {
		out = append(out, p.PID)
	}
	return out
}

func TestOrphanNeedsInitParentAndRootCwd(t *testing.T) {
	ps := `
 200     1 ??       node /work/proj/node_modules/.bin/vite --port 5173
 201     1 ??       node /work/proj/node_modules/.bin/vite
 202     1 ??       node /other/node_modules/.bin/vite
 203     1 ??       /usr/bin/vim notes.txt
 204   900 ??       node /work/proj/node_modules/.bin/vite
 900     1 ??       /usr/lib/systemd/systemd --user
 205   900 ??       npm run dev
`
	f := newFixture(t, ps,
		map[int]string{200: root + "/web", 201: "/elsewhere", 202: "/other", 203: root + "/a", 204: root + "/a", 205: root + "/a"},
		nil)
	got, err := Orphans(f.env)
	if err != nil {
		t.Fatal(err)
	}
	// 200: init parent, cwd in root, pattern. 201: cwd outside. 202: cwd outside. 203: no pattern, no port.
	// 204/205: parent is the user systemd manager, which counts as init.
	want := []int{200, 204, 205}
	if fmt.Sprint(pids(got)) != fmt.Sprint(want) {
		t.Fatalf("orphans %v, want %v", pids(got), want)
	}
	if got[0].Cwd != root+"/web" || got[0].Reason == "" || got[0].Command == "" || got[0].Start.IsZero() {
		t.Errorf("incomplete proc %+v", got[0])
	}
}

func TestShellChildNotOrphan(t *testing.T) {
	ps := `
 300  7000 ttys003  -zsh
 301   300 ttys003  node /work/proj/node_modules/.bin/vite
 302   301 ttys003  node /work/proj/node_modules/.bin/next dev
 303     1 ttys003  node /work/proj/node_modules/.bin/vite
 304     1 pts/3    node /work/proj/node_modules/.bin/vite
`
	f := newFixture(t, ps, map[int]string{300: root + "/a", 301: root + "/a", 302: root + "/a", 303: root + "/a", 304: root + "/a"},
		map[int][]string{301: {"*:5173"}, 303: {"*:5174"}})
	got, err := Orphans(f.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("shell children reported as orphans: %v", pids(got))
	}
}

func TestListenerInRootIsOrphan(t *testing.T) {
	ps := `
 400     1 ??       ./my-custom-server
 401     1 ??       ./quiet-helper
`
	f := newFixture(t, ps, map[int]string{400: root + "/a", 401: root + "/a"},
		map[int][]string{400: {"*:8080", "[::1]:8080", "127.0.0.1:9000"}})
	got, err := Orphans(f.env)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(pids(got)) != "[400]" {
		t.Fatalf("orphans %v", pids(got))
	}
	if fmt.Sprint(got[0].Ports) != "[8080 9000]" {
		t.Errorf("ports %v", got[0].Ports)
	}
}

func TestPatterns(t *testing.T) {
	yes := []string{
		"node /x/node_modules/.bin/vite", "node /x/next dev", "node /x/next start", "node nuxt dev", "node webpack serve",
		"node storybook dev", "node --inspect /x/vite.js --port 5173",
		"npm run dev", "pnpm run start", "bun run serve", "python3 -m http.server 8000", "/venv/bin/uvicorn app:app",
		"flask run", "go run ./cmd/x", "/go/bin/air", "kubectl port-forward svc/db 5432",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --headless --remote-debugging-port=9222",
		"chromium --remote-debugging-port=9222 --headless", "playwright test --ui", "node /x/playwright test --ui",
	}
	no := []string{"node server.js", "vim vite.config.js", "chrome --headless", "python -m pytest", "kubectl get pods",
		"pnpm dev", "pnpm install", "bun test", "bun run build", "npm run build", "npm install", "flask routes",
		"python -m uvicorn app:app", "go build ./...", "go test ./...", "node /x/next build", "node /x/vite build",
		"node /x/webpack", "playwright test", "playwright install", "vim next dev", "echo npm run dev",
		"someapp --remote-debugging-port=9222 --headless", "/bin/sh -c vite", "node"}
	for _, c := range yes {
		if !matchesPattern(c) {
			t.Errorf("%q should match", c)
		}
	}
	for _, c := range no {
		if matchesPattern(c) {
			t.Errorf("%q should not match", c)
		}
	}
}

func TestEngineNeverGetsProcessCandidates(t *testing.T) {
	ps := `
 500     1 ??       node /work/proj/node_modules/.bin/vite
`
	f := newFixture(t, ps, map[int]string{500: root + "/a"}, map[int][]string{500: {"*:5173"}})
	f.env.Settings = &testutil.FakeSettings{ConsentAll: true, Categories: map[string]bool{"processes": true}}
	f.env.Platform = &testutil.FakePlatform{Free: 100 << 30}
	cat := &category{}
	if cat.Name() != "processes" || cat.Bucket() != core.Optional {
		t.Fatalf("category %s %s", cat.Name(), cat.Bucket())
	}
	cs, ks, err := cat.Scan(f.env)
	if err != nil || len(cs) != 0 || len(ks) != 0 {
		t.Fatalf("scan gave %v %v %v", cs, ks, err)
	}
	s := engine.Plan(f.env, []core.Category{cat}, engine.Options{})
	if len(s.WouldRemove) != 0 || len(s.Removed) != 0 {
		t.Fatalf("engine got candidates: %+v", s)
	}
	for _, c := range f.run.Calls {
		if len(c.Argv) > 0 && (c.Argv[0] == "kill" || c.Argv[0] == "ps") {
			t.Errorf("scan ran %v", c.Argv)
		}
	}
}

// --- stop ---

type stopFixture struct {
	*fixture
	signals []syscall.Signal
	dead    bool
}

func newStop(t *testing.T, die bool) *stopFixture {
	t.Helper()
	ps := " 600     1 ??       node /work/proj/node_modules/.bin/vite\n"
	f := newFixture(t, ps, map[int]string{600: root + "/a"}, nil)
	f.run.On(reReadPrefix, "node /work/proj/node_modules/.bin/vite\n", nil)
	s := &stopFixture{fixture: f}
	oldSig, oldAlive, oldWait, oldPoll := signalPID, alive, waitExit, pollEvery
	signalPID = func(pid int, sig syscall.Signal) error {
		s.signals = append(s.signals, sig)
		if die {
			s.dead = true
		}
		return nil
	}
	alive = func(int) bool { return !s.dead }
	waitExit, pollEvery = 50*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { signalPID, alive, waitExit, pollEvery = oldSig, oldAlive, oldWait, oldPoll })
	return s
}

func fakeTerminal(t *testing.T, input string) {
	t.Helper()
	old, oldStdio := confirm.OpenTTY, confirm.StdioIsTerminal
	confirm.OpenTTY = func() (io.ReadWriteCloser, error) { return &rwc{strings.NewReader(input)}, nil }
	confirm.StdioIsTerminal = func() bool { return true }
	t.Cleanup(func() { confirm.OpenTTY, confirm.StdioIsTerminal = old, oldStdio })
}

type rwc struct{ r io.Reader }

func (r *rwc) Read(p []byte) (int, error)  { return r.r.Read(p) }
func (r *rwc) Write(p []byte) (int, error) { return len(p), nil }
func (r *rwc) Close() error                { return nil }

func TestStopSendsTermOnly(t *testing.T) {
	s := newStop(t, true)
	fakeTerminal(t, "600\n")
	exited, err := Stop(s.env, 600)
	if err != nil || !exited {
		t.Fatalf("exited=%v err=%v", exited, err)
	}
	if len(s.signals) != 1 || s.signals[0] != syscall.SIGTERM {
		t.Fatalf("signals %v", s.signals)
	}
}

func TestStopReportsStillRunningWithoutKill(t *testing.T) {
	s := newStop(t, false)
	fakeTerminal(t, "600\n")
	exited, err := Stop(s.env, 600)
	if err != nil || exited {
		t.Fatalf("exited=%v err=%v", exited, err)
	}
	if len(s.signals) != 1 || s.signals[0] != syscall.SIGTERM {
		t.Fatalf("signals %v", s.signals)
	}
}

func TestStopRefusesChangedStartTime(t *testing.T) {
	s := newStop(t, true)
	fakeTerminal(t, "600\n")
	n := 0
	StartOf = func(int) (time.Time, error) {
		n++
		return time.Date(2026, 11, 23, 9, 0, n, 0, time.UTC), nil // a different start on every read
	}
	_, err := Stop(s.env, 600)
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("got %v, want ErrChanged", err)
	}
	if len(s.signals) != 0 {
		t.Fatalf("signalled after a change: %v", s.signals)
	}
}

func TestStopRefusesChangedCommand(t *testing.T) {
	s := newStop(t, true)
	s.run.On(reReadPrefix, "some other program\n", nil)
	fakeTerminal(t, "600\n")
	if _, err := Stop(s.env, 600); !errors.Is(err, ErrChanged) {
		t.Fatalf("got %v, want ErrChanged", err)
	}
	if len(s.signals) != 0 {
		t.Fatalf("signalled after a change: %v", s.signals)
	}
}

func TestStopMismatchSignalsNothing(t *testing.T) {
	s := newStop(t, true)
	fakeTerminal(t, "601\n")
	if _, err := Stop(s.env, 600); !errors.Is(err, confirm.ErrMismatch) {
		t.Fatalf("got %v", err)
	}
	if len(s.signals) != 0 {
		t.Fatalf("signals %v", s.signals)
	}
}

func TestStopRefusesNonOrphan(t *testing.T) {
	s := newStop(t, true)
	fakeTerminal(t, "1\n")
	if _, err := Stop(s.env, 1); !errors.Is(err, ErrNotOrphan) {
		t.Fatalf("got %v, want ErrNotOrphan", err)
	}
	if len(s.signals) != 0 {
		t.Fatalf("signals %v", s.signals)
	}
}

func TestStopWithoutTerminal(t *testing.T) {
	s := newStop(t, true)
	old := confirm.OpenTTY
	confirm.OpenTTY = func() (io.ReadWriteCloser, error) { return nil, errors.New("device not configured") }
	t.Cleanup(func() { confirm.OpenTTY = old })
	if _, err := Stop(s.env, 600); !errors.Is(err, confirm.ErrNoTerminal) {
		t.Fatalf("got %v, want ErrNoTerminal", err)
	}
	if len(s.signals) != 0 {
		t.Fatalf("signals %v", s.signals)
	}
}
