// Package platform is the one place that knows the operating system (darwin, linux, WSL).
package platform

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
)

// OpenTimeout bounds one open-file check. SP6 measured a worst case of 0.71 s for 50k files,
// so 20 s is generous; a timeout counts as "in use".
const OpenTimeout = 20 * time.Second

// ErrNoNotifier is returned by Notify on Linux when notify-send is not installed.
var ErrNoNotifier = errors.New("no notifier (notify-send) on PATH")

type platform struct {
	run      core.Runner
	goos     string
	procRoot string       // default /proc; tests point it at a fake tree
	tmpBase  string       // parent of claude-<uid>; default per OS
	uid      int          // default os.Getuid()
	pid      int          // default os.Getpid()
	openTTY  func() error // default opens /dev/tty read-write
}

// New returns the Platform for the running OS. Every external command goes through run.
func New(run core.Runner) core.Platform { return newPlatform(run) }

func newPlatform(run core.Runner) *platform {
	p := &platform{
		run:      run,
		goos:     runtime.GOOS,
		procRoot: "/proc",
		uid:      os.Getuid(),
		pid:      os.Getpid(),
		openTTY: func() error {
			f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if err != nil {
				return err
			}
			return f.Close()
		},
	}
	return p
}

// UserTempDir returns the user's temp directory for the running OS (spec 4.2).
func UserTempDir() (string, error) { return New(execx.Real{}).TempDir() }

func (p *platform) OS() string { return p.goos }

func testHook(name string) string {
	if os.Getenv("SAPULIDI_TEST") != "1" {
		return ""
	}
	return os.Getenv(name)
}

// ownedDir reports whether path is a real directory (not a symlink) owned by uid.
func (p *platform) ownedDir(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == p.uid
}

func (p *platform) TempDir() (string, error) {
	if d := testHook("SAPULIDI_TEST_TEMPDIR"); d != "" {
		return d, nil
	}
	if p.goos == "darwin" {
		out, err := p.run.Run(context.Background(), "", "getconf", "DARWIN_USER_TEMP_DIR")
		if err != nil {
			return "", fmt.Errorf("getconf DARWIN_USER_TEMP_DIR: %w", err)
		}
		d := strings.TrimRight(strings.TrimSpace(string(out)), "/")
		if d == "" || !filepath.IsAbs(d) {
			return "", fmt.Errorf("getconf DARWIN_USER_TEMP_DIR: unexpected output %q", out)
		}
		return d, nil
	}
	if d := os.Getenv("TMPDIR"); filepath.IsAbs(d) && p.ownedDir(d) && !inHomeWorkArea(d) {
		return filepath.Clean(d), nil
	}
	return "/tmp", nil
}

// inHomeWorkArea reports a directory that is home itself, contains home, or lies below a non-dot top-level
// folder of home (~/work, ~/Code): the user keeps work there, so it is no temp dir.
func inHomeWorkArea(d string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return true // fail closed: without a home the check cannot be made
	}
	real := func(s string) string {
		if r, err := filepath.EvalSymlinks(s); err == nil {
			return r
		}
		return filepath.Clean(s)
	}
	d, home = real(d), real(home)
	if d == home {
		return true
	}
	rel, err := filepath.Rel(d, home)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true // d contains home
	}
	rel, err = filepath.Rel(home, d)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false // outside home
	}
	return !strings.HasPrefix(rel, ".")
}

func (p *platform) ScratchBases() []string {
	var cands []string
	if s := testHook("SAPULIDI_TEST_SCRATCH"); s != "" {
		cands = strings.Split(s, ":")
	} else {
		base := p.tmpBase
		if base == "" {
			base = "/tmp"
			if p.goos == "darwin" {
				base = "/private/tmp"
			}
		}
		cands = append(cands, filepath.Join(base, "claude-"+strconv.Itoa(p.uid)))
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			cands = append(cands, filepath.Join(home, ".codex", "tmp"))
		}
	}
	var out []string
	for _, c := range cands {
		if c != "" && p.ownedDir(c) {
			out = append(out, c)
		}
	}
	return out
}

func (p *platform) IsOpen(path string) (bool, error) {
	if p.goos == "darwin" {
		return p.isOpenLsof(path)
	}
	return p.isOpenProc(path)
}

func (p *platform) isOpenLsof(path string) (bool, error) {
	if _, err := p.run.LookPath("lsof"); err != nil {
		return true, fmt.Errorf("lsof not available: %w", err)
	}
	argv := []string{"lsof", "-nP", "-t", "--", path}
	if fi, err := os.Lstat(path); err == nil && fi.IsDir() {
		// SP6: lsof +D is silent with exit 1 on a directory it cannot read, the same as
		// "no process uses it". Fail closed (spec S7): an unreadable directory is in use.
		if _, err := os.ReadDir(path); err != nil {
			return true, fmt.Errorf("unknown (permission): %w", err)
		}
		argv = []string{"lsof", "-nP", "-t", "+D", path}
	}
	ctx, cancel := context.WithTimeout(context.Background(), OpenTimeout)
	defer cancel()
	out, err := p.run.Run(ctx, "", argv...)
	if len(strings.TrimSpace(string(out))) > 0 {
		return true, nil
	}
	if err == nil {
		return false, nil
	}
	var ee *execx.ExitError
	if errors.As(err, &ee) && ee.Code == 1 && ctx.Err() == nil {
		return false, nil
	}
	return true, fmt.Errorf("lsof: %w", err)
}

// procPIDs lists the numeric entries of procRoot that belong to the user.
func (p *platform) procPIDs() ([]string, error) {
	ents, err := os.ReadDir(p.procRoot)
	if err != nil {
		return nil, err
	}
	var pids []string
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		fi, err := os.Lstat(filepath.Join(p.procRoot, e.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue // exited since the listing
		}
		if err != nil {
			return nil, err
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != p.uid {
			continue
		}
		pids = append(pids, e.Name())
	}
	return pids, nil
}

func under(target, path string) bool {
	target = strings.TrimSuffix(target, " (deleted)")
	path = filepath.Clean(path)
	return target == path || strings.HasPrefix(target, strings.TrimRight(path, "/")+"/")
}

// gone reports that a /proc entry vanished because its process exited while it was being scanned: reading
// it then fails with ENOENT or ESRCH. Any other error (permission, I/O) is still unknown, and fails closed.
func gone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}

func (p *platform) isOpenProc(path string) (bool, error) {
	pids, err := p.procPIDs()
	if err != nil {
		return true, fmt.Errorf("read %s: %w", p.procRoot, err)
	}
	for _, pid := range pids {
		var open bool
		// A process that is being exec'd or is exiting briefly denies access to its own /proc entries, even
		// to its owner. Look again a few times; a denial that lasts is unknown and fails closed (spec S7).
		for attempt := 0; ; attempt++ {
			open, err = p.pidHolds(pid, path)
			if err == nil || !errors.Is(err, fs.ErrPermission) || attempt == procRetries {
				break
			}
			time.Sleep(procRetryWait)
		}
		if err != nil || open {
			return true, err
		}
	}
	return false, nil
}

const (
	procRetries   = 5
	procRetryWait = 20 * time.Millisecond
)

// pidHolds reports whether the process holds a file or its cwd under path.
func (p *platform) pidHolds(pid, path string) (bool, error) {
	d := filepath.Join(p.procRoot, pid)
	t, err := os.Readlink(filepath.Join(d, "cwd"))
	if err == nil && under(t, path) {
		return true, nil
	}
	if err != nil && !gone(err) {
		return true, fmt.Errorf("read cwd of process %s: %w", pid, err)
	}
	fds, err := os.ReadDir(filepath.Join(d, "fd"))
	if gone(err) {
		return false, nil // process exited
	}
	if err != nil {
		return true, fmt.Errorf("read fds of process %s: %w", pid, err)
	}
	for _, fd := range fds {
		t, err := os.Readlink(filepath.Join(d, "fd", fd.Name()))
		if gone(err) {
			continue
		}
		if err != nil {
			return true, fmt.Errorf("read fd of process %s: %w", pid, err)
		}
		if under(t, path) {
			return true, nil
		}
	}
	return false, nil
}

func (p *platform) ArgsMention(path string) (bool, error) {
	if p.goos == "darwin" {
		out, err := p.run.Run(context.Background(), "", "ps", "-axo", "pid=,uid=,command=")
		if err != nil {
			return true, fmt.Errorf("ps: %w", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			pidCol, rest := nextField(line)
			uidCol, args := nextField(rest)
			if args == "" {
				continue
			}
			pid, _ := strconv.Atoi(pidCol)
			uid, _ := strconv.Atoi(uidCol)
			if pid == p.pid || uid != p.uid {
				continue
			}
			if strings.Contains(args, path) {
				return true, nil
			}
		}
		return false, nil
	}
	pids, err := p.procPIDs()
	if err != nil {
		return true, fmt.Errorf("read %s: %w", p.procRoot, err)
	}
	for _, pid := range pids {
		if n, _ := strconv.Atoi(pid); n == p.pid {
			continue
		}
		b, err := os.ReadFile(filepath.Join(p.procRoot, pid, "cmdline"))
		if err != nil {
			continue
		}
		if strings.Contains(strings.ReplaceAll(string(b), "\x00", " "), path) {
			return true, nil
		}
	}
	return false, nil
}

// nextField splits off the first space-separated column and the single run of spaces after it;
// the remainder is returned raw.
func nextField(s string) (field, rest string) {
	s = strings.TrimLeft(s, " ")
	i := strings.IndexByte(s, ' ')
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i:], " ")
}

func (p *platform) SameDevice(a, b string) (bool, error) {
	fa, err := os.Lstat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Lstat(b)
	if err != nil {
		return false, err
	}
	sa, ok1 := fa.Sys().(*syscall.Stat_t)
	sb, ok2 := fb.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false, fmt.Errorf("no device information for %s or %s", a, b)
	}
	return sa.Dev == sb.Dev, nil
}

func (p *platform) OwnedByUser(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("no owner information for %s", path)
	}
	return int(st.Uid) == p.uid, nil
}

// FreeBytes is statfs available blocks; on macOS this excludes purgeable space (spec 10), as intended.
func (p *platform) FreeBytes(path string) (uint64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, err
	}
	return uint64(s.Bavail) * uint64(s.Bsize), nil
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (p *platform) Notify(title, body string) error {
	if p.goos == "darwin" {
		script := "display notification " + quote(body) + " with title " + quote(title)
		if _, err := p.run.Run(context.Background(), "", "osascript", "-e", script); err != nil {
			return fmt.Errorf("osascript: %w", err)
		}
		return nil
	}
	if _, err := p.run.LookPath("notify-send"); err != nil {
		return ErrNoNotifier
	}
	if _, err := p.run.Run(context.Background(), "", "notify-send", "--", title, body); err != nil {
		return fmt.Errorf("notify-send: %w", err)
	}
	return nil
}

func (p *platform) HasTTY() bool { return p.openTTY() == nil }

func (p *platform) IsWSL() bool {
	if p.goos != "linux" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(p.procRoot, "sys/kernel/osrelease"))
	if err != nil {
		return false
	}
	s := strings.ToLower(string(b))
	return strings.Contains(s, "microsoft") || strings.Contains(s, "wsl")
}
