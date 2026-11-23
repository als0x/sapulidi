package procs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/testutil"
)

// oneProc is a fixture with a single init-parented process 800 running cmd in cwd, listening on port 9999.
func oneProc(t *testing.T, cmd, cwd string) *fixture {
	t.Helper()
	return newFixture(t, fmt.Sprintf(" 800     1 ??       %s\n", cmd), map[int]string{800: cwd}, map[int][]string{800: {"*:9999"}})
}

func listedPIDs(t *testing.T, f *fixture) []int {
	t.Helper()
	got, err := Orphans(f.env)
	if err != nil {
		t.Fatal(err)
	}
	return pids(got)
}

func TestListenerExclusions(t *testing.T) {
	excluded := []string{
		"/usr/lib/postgresql/16/bin/postgres -D /data", "pg_ctl start", "redis-server *:6379", "/usr/sbin/mysqld", "mongod --dbpath x",
		"docker run x", "/usr/bin/podman machine", "qemu-system-aarch64 -m 2G", "tmux: server", "screen -S x", "ssh -L 80:x:80 h",
		"ssh-agent -l", "/usr/sbin/sshd -D", "gopls serve", "rust-analyzer", "tsserver --stdio", "code --folder-uri x",
		"/opt/Cursor/cursor", "claude --resume", "codex exec", "herdr server",
	}
	for _, cmd := range excluded {
		if got := listedPIDs(t, oneProc(t, cmd, root+"/a")); len(got) != 0 {
			t.Errorf("%q listed as orphan", cmd)
		}
	}
	for _, cmd := range []string{"./my-server --port 9999", "/usr/bin/python3 serve.py", "sshfoo"} {
		if got := listedPIDs(t, oneProc(t, cmd, root+"/a")); len(got) != 1 {
			t.Errorf("%q not listed", cmd)
		}
	}
}

func TestExclusionDoesNotHideDevServer(t *testing.T) {
	// the exclusion list only applies to the listener branch
	if got := listedPIDs(t, oneProc(t, "node /work/proj/node_modules/.bin/vite", root+"/a")); len(got) != 1 {
		t.Fatal("vite not listed")
	}
}

func TestCwdStrictlyBelowRoot(t *testing.T) {
	for _, cwd := range []string{root, root + "/", "/", "", "/work", "/work/projx/a"} {
		f := oneProc(t, "node /x/vite", cwd)
		if got := listedPIDs(t, f); len(got) != 0 {
			t.Errorf("cwd %q listed", cwd)
		}
	}
	f := oneProc(t, "node /x/vite", root+"/h")
	f.env.Home = root + "/h" // a root that contains the home: the home itself still never counts
	if got := listedPIDs(t, f); len(got) != 0 {
		t.Error("cwd equal to home listed")
	}
	f = oneProc(t, "node /x/vite", root+"/h/x")
	f.env.Home = root + "/h"
	if got := listedPIDs(t, f); len(got) != 1 {
		t.Error("cwd below home and root not listed")
	}
}

func TestLsofExitOneEmptyMeansNoListeners(t *testing.T) {
	f := oneProc(t, "node /x/vite", root+"/a")
	f.run.On(portPrefix, "", &execx.ExitError{Code: 1})
	r, err := List(f.env)
	if err != nil || len(r.Warnings) != 0 || len(r.Procs) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestLsofFailureSkipsPortBranchAndWarns(t *testing.T) {
	for name, perr := range map[string]error{
		"timeout":        errors.New("lsof: context deadline exceeded"),
		"exit 2":         &execx.ExitError{Code: 2, Stderr: "permission"},
		"exit 1 w/ data": &execx.ExitError{Code: 1},
	} {
		// two processes: one dev server by pattern, one plain listener
		ps := " 810     1 ??       node /x/vite\n 811     1 ??       ./plain-listener\n"
		f := newFixture(t, ps, map[int]string{810: root + "/a", 811: root + "/a"}, nil)
		out := ""
		if name == "exit 1 w/ data" {
			out = "p811\nn*:7000\n"
		}
		f.run.On(portPrefix, out, perr)
		r, err := List(f.env)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(r.Warnings) != 1 || r.Warnings[0] != PortsUnchecked {
			t.Errorf("%s: warnings %v", name, r.Warnings)
		}
		if fmt.Sprint(pids(r.Procs)) != "[810]" {
			t.Errorf("%s: procs %v", name, pids(r.Procs))
		}
	}
}

func TestLsofCwdFailureIsAnError(t *testing.T) {
	f := oneProc(t, "node /x/vite", root+"/a")
	f.run.On(cwdPrefix, "", errors.New("lsof: boom"))
	if _, err := Orphans(f.env); err == nil {
		t.Fatal("cwd failure ignored")
	}
}

// --- stop ---

func TestStopRefusesSelfAndParent(t *testing.T) {
	s := newStop(t, true)
	oldSelf := self
	self = func() (int, int) { return 600, 601 }
	t.Cleanup(func() { self = oldSelf })
	for _, pid := range []int{600, 601} {
		fakeTerminal(t, fmt.Sprintf("%d\n", pid))
		if _, err := Stop(s.env, pid); !errors.Is(err, ErrProtected) {
			t.Errorf("pid %d: got %v, want ErrProtected", pid, err)
		}
	}
	if len(s.signals) != 0 {
		t.Fatalf("signals %v", s.signals)
	}
}

// flipRunner answers the process listing differently from its second call on.
type flipRunner struct {
	*testutil.FakeRunner
	later string
	n     int
}

func (r *flipRunner) Run(ctx context.Context, dir string, argv ...string) ([]byte, error) {
	if len(argv) > 2 && argv[0] == "ps" && argv[2] == "pid=,ppid=,tty=,command=" {
		r.n++
		if r.n >= 2 {
			return []byte(r.later), nil
		}
	}
	return r.FakeRunner.Run(ctx, dir, argv...)
}

func TestStopRerunsTheListing(t *testing.T) {
	for name, later := range map[string]string{
		"command changed": " 600     1 ??       node /work/proj/node_modules/.bin/vite --other\n",
		"process gone":    "",
	} {
		s := newStop(t, true)
		s.env.Run = &flipRunner{FakeRunner: s.run, later: later}
		fakeTerminal(t, "600\n")
		if _, err := Stop(s.env, 600); !errors.Is(err, ErrChanged) {
			t.Errorf("%s: got %v, want ErrChanged", name, err)
		}
		if len(s.signals) != 0 {
			t.Errorf("%s: signals %v", name, s.signals)
		}
	}
}

func TestStopRereadUsesWideOutput(t *testing.T) {
	s := newStop(t, true)
	fakeTerminal(t, "600\n")
	if _, err := Stop(s.env, 600); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range s.run.Calls {
		if len(c.Argv) > 2 && c.Argv[0] == "ps" && c.Argv[2] == "command=" {
			found = true
			if !strings.Contains(strings.Join(c.Argv, " "), " -ww ") {
				t.Errorf("re-read without -ww: %v", c.Argv)
			}
		}
	}
	if !found {
		t.Error("no re-read of the command")
	}
}

// --- category ---

func TestEngineRunOnlyProcessesRemovesNothing(t *testing.T) {
	t.Setenv("SAPULIDI_TEST", "1")
	home := testutil.TempHome(t)
	st, err := state.Open(config.Paths{
		Config: filepath.Join(home, "c"), State: filepath.Join(home, "s"), Data: filepath.Join(home, "d"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveConsent(&state.Consent{At: time.Now(), Rules: []string{"x/a@1"}}); err != nil {
		t.Fatal(err)
	}
	f := oneProc(t, "node /work/proj/node_modules/.bin/vite", root+"/a")
	f.env.Home = home
	f.env.Settings = &testutil.FakeSettings{ConsentAll: true, Categories: map[string]bool{"processes": true}}
	f.env.Platform = &testutil.FakePlatform{Free: 100 << 30, Temp: filepath.Join(home, "tmp")}
	f.env.Log = &testutil.MemLogger{}
	var signals []syscall.Signal
	old := signalPID
	signalPID = func(_ int, sig syscall.Signal) error { signals = append(signals, sig); return nil }
	t.Cleanup(func() { signalPID = old })

	cat := &category{}
	if cs, ks, err := cat.Scan(f.env); err != nil || cs != nil || ks != nil {
		t.Fatalf("Scan gave %v %v %v", cs, ks, err)
	}
	s, err := engine.Run(f.env, []core.Category{cat}, st, engine.Options{Only: []string{"processes"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Removed) != 0 || len(s.WouldRemove) != 0 || len(signals) != 0 {
		t.Fatalf("engine touched something: %+v, signals %v", s, signals)
	}
}
