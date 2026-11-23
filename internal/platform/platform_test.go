package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/testutil"
)

func newTest(t *testing.T, goos string) (*platform, *testutil.FakeRunner) {
	t.Helper()
	testutil.TempHome(t)
	r := &testutil.FakeRunner{}
	p := newPlatform(r)
	p.goos = goos
	p.procRoot = filepath.Join(t.TempDir(), "proc")
	p.openTTY = func() error { return errors.New("no tty in tests") }
	return p, r
}

func TestTempDirDarwinUsesGetconf(t *testing.T) {
	p, r := newTest(t, "darwin")
	t.Setenv("TMPDIR", "/wrong/")
	r.On([]string{"getconf", "DARWIN_USER_TEMP_DIR"}, "/var/folders/xx/T/\n", nil)
	got, err := p.TempDir()
	if err != nil || got != "/var/folders/xx/T" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTempDirLinuxRejectsSymlinkAndForeignOwner(t *testing.T) {
	p, _ := newTest(t, "linux")
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", link)
	if got, _ := p.TempDir(); got != "/tmp" {
		t.Fatalf("symlink accepted: %q", got)
	}
	t.Setenv("TMPDIR", real)
	if got, _ := p.TempDir(); got != real {
		t.Fatalf("own dir rejected: %q", got)
	}
	p.uid = os.Getuid() + 1
	if got, _ := p.TempDir(); got != "/tmp" {
		t.Fatalf("foreign owner accepted: %q", got)
	}
}

func TestTempDirLinuxRefusesHomeFolder(t *testing.T) {
	p, _ := newTest(t, "linux")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	mk := func(rel string) string {
		d := filepath.Join(home, rel)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, d := range []string{home, mk("work"), mk("Code/tmp"), filepath.Dir(home)} {
		t.Setenv("TMPDIR", d)
		if got, _ := p.TempDir(); got != "/tmp" {
			t.Errorf("TMPDIR=%s accepted: %q", d, got)
		}
	}
	// a dot folder of home is a cache place, not a work folder
	dot := mk(".cache/tmp")
	t.Setenv("TMPDIR", dot)
	if got, _ := p.TempDir(); got != dot {
		t.Errorf("TMPDIR=%s refused: %q", dot, got)
	}
}

func TestTempDirLinuxFallsBackToTmp(t *testing.T) {
	p, _ := newTest(t, "linux")
	for _, v := range []string{"", "relative/dir", filepath.Join(t.TempDir(), "missing")} {
		t.Setenv("TMPDIR", v)
		if got, err := p.TempDir(); err != nil || got != "/tmp" {
			t.Fatalf("TMPDIR=%q: %q, %v", v, got, err)
		}
	}
}

func TestTestHooksIgnoredWithoutFlag(t *testing.T) {
	p, _ := newTest(t, "linux")
	dir := t.TempDir()
	t.Setenv("SAPULIDI_TEST", "")
	t.Setenv("SAPULIDI_TEST_TEMPDIR", dir)
	t.Setenv("SAPULIDI_TEST_SCRATCH", dir)
	t.Setenv("TMPDIR", "")
	if got, _ := p.TempDir(); got != "/tmp" {
		t.Fatalf("hook honoured without flag: %q", got)
	}
	for _, b := range p.ScratchBases() {
		if b == dir {
			t.Fatal("scratch hook honoured without flag")
		}
	}
	t.Setenv("SAPULIDI_TEST", "1")
	if got, _ := p.TempDir(); got != dir {
		t.Fatalf("temp hook ignored: %q", got)
	}
	if b := p.ScratchBases(); len(b) != 1 || b[0] != dir {
		t.Fatalf("scratch hook ignored: %v", b)
	}
}

func TestScratchBasesOnlyOwnedDirs(t *testing.T) {
	p, _ := newTest(t, "linux")
	tmp := t.TempDir()
	p.tmpBase = tmp
	home := os.Getenv("HOME")
	uid := os.Getuid()
	claude := filepath.Join(tmp, fmt.Sprintf("claude-%d", uid))
	codex := filepath.Join(home, ".codex", "tmp")
	if got := p.ScratchBases(); len(got) != 0 {
		t.Fatalf("missing dirs listed: %v", got)
	}
	for _, d := range []string{claude, codex} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.ScratchBases(); len(got) != 2 || got[0] != claude || got[1] != codex {
		t.Fatalf("got %v", got)
	}
	// a symlink in place of the directory is dropped
	if err := os.Remove(codex); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claude, codex); err != nil {
		t.Fatal(err)
	}
	if got := p.ScratchBases(); len(got) != 1 || got[0] != claude {
		t.Fatalf("symlink kept: %v", got)
	}
	// a foreign owner is dropped
	p.uid = uid + 1
	if got := p.ScratchBases(); len(got) != 0 {
		t.Fatalf("foreign kept: %v", got)
	}
}

func TestIsOpenDarwinMatch(t *testing.T) {
	p, r := newTest(t, "darwin")
	r.Paths = map[string]string{"lsof": "/usr/sbin/lsof"}
	dir := t.TempDir()
	r.On([]string{"lsof", "-nP", "-t", "+D", dir}, "123\n", nil)
	if open, err := p.IsOpen(dir); !open || err != nil {
		t.Fatalf("%v %v", open, err)
	}
	f := filepath.Join(dir, "f")
	testutil.Touch(t, f, 0)
	r.On([]string{"lsof", "-nP", "-t", "--", f}, "", &execx.ExitError{Code: 1})
	if open, err := p.IsOpen(f); open || err != nil {
		t.Fatalf("file: %v %v", open, err)
	}
}

func TestIsOpenDarwinNoMatch(t *testing.T) {
	p, r := newTest(t, "darwin")
	r.Paths = map[string]string{"lsof": "/usr/sbin/lsof"}
	dir := t.TempDir()
	r.On([]string{"lsof"}, "", &execx.ExitError{Code: 1})
	if open, err := p.IsOpen(dir); open || err != nil {
		t.Fatalf("%v %v", open, err)
	}
	r.On([]string{"lsof"}, "", &execx.ExitError{Code: 2, Stderr: "boom"})
	if open, err := p.IsOpen(dir); !open || err == nil {
		t.Fatalf("exit 2 must count as open: %v %v", open, err)
	}
}

func TestIsOpenUnreadableDirIsOpen(t *testing.T) {
	p, r := newTest(t, "darwin")
	r.Paths = map[string]string{"lsof": "/usr/sbin/lsof"}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("directory with mode 000 is still readable (running as root)")
	}
	r.On([]string{"lsof"}, "", &execx.ExitError{Code: 1})
	open, err := p.IsOpen(dir)
	if !open || err == nil {
		t.Fatalf("unreadable dir must count as open with an error: %v %v", open, err)
	}
	if len(r.Calls) != 0 {
		t.Fatalf("lsof must not be called for an unreadable dir: %v", r.Calls)
	}
}

func TestIsOpenDarwinMatchByStdoutNotExitCode(t *testing.T) {
	p, r := newTest(t, "darwin")
	r.Paths = map[string]string{"lsof": "/usr/sbin/lsof"}
	dir := t.TempDir()
	r.On([]string{"lsof", "-nP", "-t", "+D", dir}, "123\n", &execx.ExitError{Code: 1})
	if open, err := p.IsOpen(dir); !open || err != nil {
		t.Fatalf("exit 1 with a pid line must be open: %v %v", open, err)
	}
	empty := t.TempDir()
	r.On([]string{"lsof", "-nP", "-t", "+D", empty}, "", &execx.ExitError{Code: 1})
	if open, err := p.IsOpen(empty); open || err != nil {
		t.Fatalf("exit 1 with empty stdout on a readable dir must be not open: %v %v", open, err)
	}
}

func TestIsOpenTimeoutCountsAsOpen(t *testing.T) {
	p, r := newTest(t, "darwin")
	r.Paths = map[string]string{"lsof": "/usr/sbin/lsof"}
	r.On([]string{"lsof"}, "", fmt.Errorf("lsof: %w", context.DeadlineExceeded))
	if open, err := p.IsOpen(t.TempDir()); !open || err == nil {
		t.Fatalf("%v %v", open, err)
	}
}

func TestIsOpenMissingLsofCountsAsOpen(t *testing.T) {
	p, _ := newTest(t, "darwin")
	if open, err := p.IsOpen(t.TempDir()); !open || err == nil {
		t.Fatalf("%v %v", open, err)
	}
}

func fakeProc(t *testing.T, root string, pid int, fds map[string]string, cwd, cmdline string) {
	t.Helper()
	d := filepath.Join(root, fmt.Sprint(pid))
	if err := os.MkdirAll(filepath.Join(d, "fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range fds {
		if err := os.Symlink(target, filepath.Join(d, "fd", name)); err != nil {
			t.Fatal(err)
		}
	}
	if cwd != "" {
		if err := os.Symlink(cwd, filepath.Join(d, "cwd")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsOpenLinuxFakeProc(t *testing.T) {
	p, _ := newTest(t, "linux")
	cand := "/work/proj/build"
	fakeProc(t, p.procRoot, 100, map[string]string{"3": "/elsewhere/x", "4": "/work/proj/buildx/y"}, "/work/other", "")
	if open, err := p.IsOpen(cand); open || err != nil {
		t.Fatalf("unrelated fds matched: %v %v", open, err)
	}
	fakeProc(t, p.procRoot, 123, map[string]string{"3": cand + "/out/a.o"}, "", "")
	if open, err := p.IsOpen(cand); !open || err != nil {
		t.Fatalf("fd under path: %v %v", open, err)
	}
	fakeProc(t, p.procRoot, 124, nil, cand, "")
	if open, _ := p.IsOpen(cand); !open {
		t.Fatal("cwd not seen")
	}
}

func TestIsOpenLinuxUnreadableProcCountsAsOpen(t *testing.T) {
	p, _ := newTest(t, "linux") // procRoot does not exist
	if open, err := p.IsOpen("/x"); !open || err == nil {
		t.Fatalf("%v %v", open, err)
	}
}

func TestArgsMentionExcludesSelf(t *testing.T) {
	p, _ := newTest(t, "linux")
	p.pid = 500
	cand := "/tmp/build-1"
	fakeProc(t, p.procRoot, 500, nil, "", "sapulidi\x00check\x00"+cand)
	if m, err := p.ArgsMention(cand); m || err != nil {
		t.Fatalf("self matched: %v %v", m, err)
	}
	fakeProc(t, p.procRoot, 501, nil, "", "cc\x00-o\x00"+cand+"/x.o")
	if m, _ := p.ArgsMention(cand); !m {
		t.Fatal("not matched")
	}

	d, r := newTest(t, "darwin")
	d.pid = 500
	r.On([]string{"ps"}, fmt.Sprintf("500 %d sapulidi %s\n7 %d vim /other\n", d.uid, cand, d.uid), nil)
	if m, err := d.ArgsMention(cand); m || err != nil {
		t.Fatalf("darwin: %v %v", m, err)
	}
	r.On([]string{"ps"}, fmt.Sprintf("8 %d cc -o %s/x.o\n", d.uid, cand), nil)
	if m, _ := d.ArgsMention(cand); !m {
		t.Fatal("darwin not matched")
	}
}

func TestSameDeviceSameDir(t *testing.T) {
	p, _ := newTest(t, "linux")
	d := t.TempDir()
	if same, err := p.SameDevice(d, d); !same || err != nil {
		t.Fatalf("%v %v", same, err)
	}
	if _, err := p.SameDevice(d, filepath.Join(d, "missing")); err == nil {
		t.Fatal("missing path must error")
	}
}

func TestOwnedByUser(t *testing.T) {
	p, _ := newTest(t, "linux")
	d := t.TempDir()
	if ok, err := p.OwnedByUser(d); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if _, err := p.OwnedByUser(filepath.Join(d, "missing")); err == nil {
		t.Fatal("missing must error")
	}
}

func TestFreeBytesPositive(t *testing.T) {
	p, _ := newTest(t, "linux")
	if n, err := p.FreeBytes(t.TempDir()); err != nil || n == 0 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestNotifyQuotesTitleAndBody(t *testing.T) {
	p, r := newTest(t, "darwin")
	r.On([]string{"osascript"}, "", nil)
	if err := p.Notify(`ti"tle`, `a "b" \ c`); err != nil {
		t.Fatal(err)
	}
	got := r.Calls[0].Argv
	want := `display notification "a \"b\" \\ c" with title "ti\"tle"`
	if len(got) != 3 || got[0] != "osascript" || got[1] != "-e" || got[2] != want {
		t.Fatalf("got %q", got)
	}
	r.On([]string{"osascript"}, "", errors.New("fail"))
	if p.Notify("a", "b") == nil {
		t.Fatal("non-zero exit must be an error")
	}
}

func TestNotifyLinuxNoNotifier(t *testing.T) {
	p, r := newTest(t, "linux")
	if err := p.Notify("a", "b"); !errors.Is(err, ErrNoNotifier) {
		t.Fatalf("got %v", err)
	}
	r.Paths = map[string]string{"notify-send": "/usr/bin/notify-send"}
	r.On([]string{"notify-send"}, "", nil)
	if err := p.Notify("a", "b"); err != nil {
		t.Fatal(err)
	}
}

func TestIsWSLFromOsrelease(t *testing.T) {
	p, _ := newTest(t, "linux")
	write := func(s string) {
		if err := os.MkdirAll(filepath.Join(p.procRoot, "sys/kernel"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.procRoot, "sys/kernel/osrelease"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if p.IsWSL() {
		t.Fatal("missing file is not WSL")
	}
	write("6.6.1-microsoft-standard-WSL2\n")
	if !p.IsWSL() {
		t.Fatal("WSL2 not detected")
	}
	write("6.8.0-generic\n")
	if p.IsWSL() {
		t.Fatal("generic kernel detected as WSL")
	}
	p.goos = "darwin"
	write("Microsoft")
	if p.IsWSL() {
		t.Fatal("darwin is never WSL")
	}
}

func TestHasTTYUsesOpener(t *testing.T) {
	p, _ := newTest(t, "linux")
	if p.HasTTY() {
		t.Fatal("opener failed, want false")
	}
	p.openTTY = func() error { return nil }
	if !p.HasTTY() || !strings.Contains(p.OS(), "linux") {
		t.Fatal("want true / linux")
	}
}

func TestIsOpenLinuxUnreadableFdDirCountsAsOpen(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	p, _ := newTest(t, "linux")
	fakeProc(t, p.procRoot, 200, map[string]string{"3": "/elsewhere"}, "", "")
	fd := filepath.Join(p.procRoot, "200", "fd")
	if err := os.Chmod(fd, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(fd, 0o755) })
	if open, err := p.IsOpen("/x"); !open || err == nil {
		t.Fatalf("%v %v", open, err)
	}
}

func TestIsOpenLinuxSkipsExitedProcess(t *testing.T) {
	p, _ := newTest(t, "linux")
	// pid dir without an fd directory: the process exited between listing and reading
	if err := os.MkdirAll(filepath.Join(p.procRoot, "300"), 0o755); err != nil {
		t.Fatal(err)
	}
	if open, err := p.IsOpen("/x"); open || err != nil {
		t.Fatalf("%v %v", open, err)
	}
}

// A process that exits during the scan makes /proc/<pid>/cwd and fd fail with ESRCH, not ENOENT: it exited, so
// it holds nothing. Any other error stays unknown.
func TestGoneIsExitedProcessOnly(t *testing.T) {
	if !gone(&os.PathError{Op: "readlink", Path: "/proc/1/cwd", Err: syscall.ESRCH}) {
		t.Fatal("ESRCH must count as exited")
	}
	if !gone(&os.PathError{Op: "readlink", Path: "/proc/1/cwd", Err: syscall.ENOENT}) {
		t.Fatal("ENOENT must count as exited")
	}
	if gone(&os.PathError{Op: "readlink", Path: "/proc/1/cwd", Err: syscall.EACCES}) || gone(nil) {
		t.Fatal("permission errors and nil are not an exited process")
	}
}

func TestIsOpenLinuxTrailingSlashMatchesCwd(t *testing.T) {
	p, _ := newTest(t, "linux")
	fakeProc(t, p.procRoot, 400, nil, "/work/proj/build", "")
	if open, err := p.IsOpen("/work/proj/build/"); !open || err != nil {
		t.Fatalf("%v %v", open, err)
	}
	if !under("/a/b/c", "/a/b/") || under("/a/bc", "/a/b/") {
		t.Fatal("under with trailing slash")
	}
}

func TestIsOpenDarwinExitTwoEmptyOutputCountsAsOpen(t *testing.T) {
	p, r := newTest(t, "darwin")
	r.Paths = map[string]string{"lsof": "/usr/sbin/lsof"}
	r.On([]string{"lsof"}, "", &execx.ExitError{Code: 2})
	if open, err := p.IsOpen(t.TempDir()); !open || err == nil {
		t.Fatalf("%v %v", open, err)
	}
}

func TestArgsMentionDarwinPsFailureCountsAsMentioned(t *testing.T) {
	p, r := newTest(t, "darwin")
	r.On([]string{"ps"}, "", errors.New("ps failed"))
	if m, err := p.ArgsMention("/x"); !m || err == nil {
		t.Fatalf("%v %v", m, err)
	}
}

func TestArgsMentionDarwinKeepsRawWhitespace(t *testing.T) {
	p, r := newTest(t, "darwin")
	p.pid = 1
	r.On([]string{"ps"}, fmt.Sprintf("  8   %d cc -o /tmp/a  b/x.o\n9 %d cc /tmp/c\td/y\n", p.uid, p.uid), nil)
	for _, path := range []string{"/tmp/a  b", "/tmp/c\td"} {
		if m, err := p.ArgsMention(path); !m || err != nil {
			t.Fatalf("%q: %v %v", path, m, err)
		}
	}
}

func TestNotifyLinuxSendsDashDash(t *testing.T) {
	p, r := newTest(t, "linux")
	r.Paths = map[string]string{"notify-send": "/usr/bin/notify-send"}
	r.On([]string{"notify-send"}, "", nil)
	if err := p.Notify("-t", "-b"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.Calls[0].Argv, " "); got != "notify-send -- -t -b" {
		t.Fatalf("got %q", got)
	}
}
