// Package procs finds processes that agents left behind and stops one after a terminal confirmation.
package procs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
)

// Proc is one orphaned process.
type Proc struct {
	PID     int
	Start   time.Time
	Command string
	Cwd     string
	Ports   []int
	Reason  string
}

// Result is a listing plus what could not be checked while making it.
type Result struct {
	Procs    []Proc
	Warnings []string
}

// PortsUnchecked is the warning given when lsof could not list the listening ports.
const PortsUnchecked = "could not check listening ports"

var (
	// ErrNotOrphan means the pid is not in the current orphan list.
	ErrNotOrphan = errors.New("not an orphaned process under a root")
	// ErrChanged means the process is no longer the one that was listed (pid reuse or a new command).
	ErrChanged = errors.New("process changed since it was listed")
	// ErrProtected means the pid is this program or its parent.
	ErrProtected = errors.New("refusing to stop this program or its parent")
)

// Seams for tests; the defaults are the real thing.
var (
	// StartOf reads a process start time; tests of other packages replace it.
	StartOf   = execx.ProcessStart
	signalPID = func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }
	alive     = func(pid int) bool { return syscall.Kill(pid, 0) == nil }
	self      = func() (pid, ppid int) { return os.Getpid(), os.Getppid() }
	waitExit  = 10 * time.Second
	pollEvery = 100 * time.Millisecond
)

type row struct {
	pid, ppid int
	tty, cmd  string
}

// Orphans lists processes of the user that nobody is waiting for, under a root, that look like dev servers
// or helpers (or listen on a TCP port).
func Orphans(env *core.Env) ([]Proc, error) {
	r, err := List(env)
	return r.Procs, err
}

// List is Orphans plus warnings about what could not be checked.
func List(env *core.Env) (Result, error) {
	var res Result
	if _, err := env.Run.LookPath("lsof"); err != nil {
		return res, fmt.Errorf("lsof is needed to read process directories: %w", err)
	}
	uid := strconv.Itoa(os.Getuid())
	out, err := env.Run.Run(env.Ctx, "", "ps", "-o", "pid=,ppid=,tty=,command=", "-ww", "-u", uid)
	if err != nil {
		return res, fmt.Errorf("ps: %w", err)
	}
	rows := map[int]row{}
	for _, line := range strings.Split(string(out), "\n") {
		if r, ok := parseRow(line); ok {
			rows[r.pid] = r
		}
	}
	cwdOut, err := env.Run.Run(env.Ctx, "", "lsof", "-nP", "-a", "-d", "cwd", "-Fpn", "-u", uid)
	if lsofFailed(cwdOut, err) {
		return res, fmt.Errorf("lsof: %w", err)
	}
	portOut, err := env.Run.Run(env.Ctx, "", "lsof", "-nP", "-a", "-iTCP", "-sTCP:LISTEN", "-Fpn", "-u", uid)
	portsOK := !lsofFailed(portOut, err)
	if !portsOK {
		res.Warnings = append(res.Warnings, PortsUnchecked)
	}
	cwds := parseLsof(cwdOut)
	listen := map[int][]string{}
	if portsOK {
		listen = parseLsof(portOut)
	}

	for _, r := range rows {
		if hasTTY(r.tty) || !initLike(r.ppid, rows) {
			continue // part of an open terminal session, or a child of a live process
		}
		cwd := ""
		if v := cwds[r.pid]; len(v) > 0 {
			cwd = v[0]
		}
		if !strictlyUnderRoot(env, cwd) {
			continue
		}
		ports := portsOf(listen[r.pid])
		reason := ""
		switch {
		case matchesPattern(r.cmd):
			reason = "dev server or helper left running"
		case len(ports) > 0 && !excludedListener(r.cmd):
			reason = "listens on port " + strconv.Itoa(ports[0])
		default:
			continue
		}
		start, err := StartOf(r.pid)
		if err != nil {
			continue // gone while we were looking
		}
		res.Procs = append(res.Procs, Proc{PID: r.pid, Start: start, Command: r.cmd, Cwd: cwd, Ports: ports, Reason: reason})
	}
	sort.Slice(res.Procs, func(i, j int) bool { return res.Procs[i].PID < res.Procs[j].PID })
	return res, nil
}

// lsofFailed: exit status 1 with no output is lsof's way of saying "nothing found"; any other error is a failure.
func lsofFailed(out []byte, err error) bool {
	if err == nil {
		return false
	}
	var ee *execx.ExitError
	return !(errors.As(err, &ee) && ee.Code == 1 && len(out) == 0)
}

// hasTTY: macOS prints ?? and Linux ? for a process without a controlling terminal.
func hasTTY(tty string) bool { return tty != "??" && tty != "?" }

func parseRow(line string) (row, bool) {
	f := strings.Fields(line)
	if len(f) < 4 {
		return row{}, false
	}
	pid, err1 := strconv.Atoi(f[0])
	ppid, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return row{}, false
	}
	rest := strings.TrimSpace(line)
	for i := range 3 { // drop pid, ppid, tty
		rest = strings.TrimSpace(rest[len(f[i]):])
	}
	return row{pid: pid, ppid: ppid, tty: f[2], cmd: rest}, true
}

// initLike: the parent is PID 1 or the user's launchd/systemd manager.
func initLike(ppid int, rows map[int]row) bool {
	if ppid == 1 {
		return true
	}
	p, ok := rows[ppid]
	if !ok {
		return false
	}
	switch exeBase(p.cmd) {
	case "systemd", "launchd":
		return true
	}
	return false
}

// strictlyUnderRoot: dir is inside a root, not the root itself, not the home directory, not "/".
func strictlyUnderRoot(env *core.Env, dir string) bool {
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	if dir == "/" || (env.Home != "" && dir == filepath.Clean(env.Home)) {
		return false
	}
	for _, r := range env.Roots {
		if strings.HasPrefix(dir, strings.TrimSuffix(r.Path, "/")+"/") {
			return true
		}
	}
	return false
}

// parseLsof reads -Fpn output: "p<pid>" lines, each followed by "n<name>" lines.
func parseLsof(out []byte) map[int][]string {
	res := map[int][]string{}
	pid := 0
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n") && pid > 0:
			res[pid] = append(res[pid], line[1:])
		}
	}
	return res
}

func portsOf(names []string) []int {
	seen := map[int]bool{}
	var ports []int
	for _, n := range names {
		i := strings.LastIndex(n, ":")
		if i < 0 {
			continue
		}
		p, err := strconv.Atoi(n[i+1:])
		if err != nil || seen[p] {
			continue
		}
		seen[p] = true
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// Stop stops one orphan: the user types the pid in a real terminal, the listing is made again and must still
// hold the same process (pid, start, command), then SIGTERM is sent. It reports whether the process exited
// within 10 s. There is no SIGKILL.
func Stop(env *core.Env, pid int) (exited bool, err error) {
	if me, parent := self(); pid == me || pid == parent {
		return false, ErrProtected
	}
	list, err := Orphans(env)
	if err != nil {
		return false, err
	}
	listed := find(list, pid)
	if listed == nil {
		return false, ErrNotOrphan
	}
	if err := confirm.Terminal("type the pid to stop it: ", strconv.Itoa(pid)); err != nil {
		return false, err
	}
	again, err := Orphans(env)
	if err != nil {
		return false, err
	}
	now := find(again, pid)
	if now == nil || !now.Start.Equal(listed.Start) || now.Command != listed.Command {
		return false, ErrChanged
	}
	out, err := env.Run.Run(env.Ctx, "", "ps", "-o", "command=", "-ww", "-p", strconv.Itoa(pid))
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrChanged, err)
	}
	if strings.TrimSpace(string(out)) != listed.Command {
		return false, ErrChanged
	}
	if err := signalPID(pid, syscall.SIGTERM); err != nil {
		return false, err
	}
	deadline := time.Now().Add(waitExit)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true, nil
		}
		time.Sleep(pollEvery)
	}
	return !alive(pid), nil
}

func find(list []Proc, pid int) *Proc {
	for i := range list {
		if list[i].PID == pid {
			return &list[i]
		}
	}
	return nil
}

// category is the "processes" entry of the report. It is REPORT-ONLY by design (spec S12): Scan never gives
// the engine a candidate, so no run, plan or schedule can ever stop a process; only `sapulidi procs stop`
// can, after a confirmation typed in a real terminal.
type category struct{}

func (*category) Name() string        { return "processes" }
func (*category) Bucket() core.Bucket { return core.Optional }
func (*category) Scan(*core.Env) ([]core.Candidate, []core.Kept, error) {
	return nil, nil, nil
}
func (*category) Proof(*core.Env, core.Candidate) error { return nil }

func init() { core.Register(&category{}) }

// exeBase is the lowercase executable name of a command line: first word, without directory, without a
// trailing colon (tmux: server).
func exeBase(cmd string) string {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(filepath.Base(f[0]), ":"))
}
