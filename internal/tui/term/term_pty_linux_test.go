//go:build linux

package term

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const (
	tiocgptn   = 0x80045430
	tiocsptlck = 0x40045431
	tiocswinsz = 0x5414
)

type winsize struct{ Row, Col, X, Y uint16 }

// openPTY returns both ends of a fresh pseudo-terminal, without any dependency.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	var unlock int32
	if err := ioctl(m.Fd(), tiocsptlck, unsafe.Pointer(&unlock)); err != nil {
		t.Fatalf("TIOCSPTLCK: %v", err)
	}
	var n uint32
	if err := ioctl(m.Fd(), tiocgptn, unsafe.Pointer(&n)); err != nil {
		t.Fatalf("TIOCGPTN: %v", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}

func setWinsize(t *testing.T, master *os.File, cols, rows int) {
	t.Helper()
	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	if err := ioctl(master.Fd(), tiocswinsz, unsafe.Pointer(&ws)); err != nil {
		t.Fatal(err)
	}
}

// capture collects everything the terminal under test writes, read from the master in a goroutine
// (the master is blocking, so a read deadline is not available).
type capture struct {
	mu sync.Mutex
	b  strings.Builder
}

func startCapture(master *os.File) *capture {
	c := &capture{}
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := master.Read(buf)
			c.mu.Lock()
			c.b.Write(buf[:n])
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}

func (c *capture) waitFor(t *testing.T, sub string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(c.String(), sub) {
		if time.Now().After(deadline) {
			t.Fatalf("output %q never contained %q", c.String(), sub)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mustTermios(t *testing.T, f *os.File) syscall.Termios {
	t.Helper()
	tm, err := getTermios(f)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func nextEvent(t *testing.T, tm *Term) Event {
	t.Helper()
	select {
	case e := <-tm.Events():
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event within 2s")
		return nil
	}
}

func TestPTYEnterRestore(t *testing.T) {
	master, slave := openPTY(t)
	out := startCapture(master)
	orig := mustTermios(t, slave)
	tm := newTerm(slave, slave)

	if err := tm.Enter(); err != nil {
		t.Fatal(err)
	}
	raw := mustTermios(t, slave)
	if raw.Lflag&(syscall.ICANON|syscall.ECHO|syscall.ISIG) != 0 {
		t.Fatalf("slave is not raw after Enter: lflag=%#x", raw.Lflag)
	}
	out.waitFor(t, enterSeq)
	tm.Restore()
	tm.Restore() // idempotent
	if got := mustTermios(t, slave); got != orig {
		t.Fatalf("termios after Restore differs from the original:\n got %+v\nwant %+v", got, orig)
	}
	out.waitFor(t, restoreSeq)
	if n := strings.Count(out.String(), restoreSeq); n != 1 {
		t.Fatalf("restore sequence written %d times, want 1", n)
	}
}

func TestPTYEnterTwiceAndAfterRestore(t *testing.T) {
	_, slave := openPTY(t)
	orig := mustTermios(t, slave)
	tm := newTerm(slave, slave)
	if err := tm.Enter(); err != nil {
		t.Fatal(err)
	}
	if err := tm.Enter(); err != nil {
		t.Fatalf("second Enter should do nothing, got %v", err)
	}
	tm.Restore()
	if got := mustTermios(t, slave); got != orig {
		t.Fatalf("one Restore after two Enters did not restore the original mode")
	}
	if err := tm.Enter(); err == nil {
		t.Fatal("Enter after Restore must fail")
	}
	if got := mustTermios(t, slave); got != orig {
		t.Fatal("failed Enter changed the terminal")
	}
}

func TestPTYKeysAndSize(t *testing.T) {
	master, slave := openPTY(t)
	setWinsize(t, master, 80, 24)
	tm := newTerm(slave, slave)
	if cols, rows, err := tm.Size(); err != nil || cols != 80 || rows != 24 {
		t.Fatalf("Size = %d, %d, %v; want 80, 24", cols, rows, err)
	}
	if err := tm.Enter(); err != nil {
		t.Fatal(err)
	}
	defer tm.Restore()

	if _, err := master.WriteString("\x1b[Aq\x03"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []Key{{Special: KeyUp}, {Rune: 'q'}, {Rune: 'c', Ctrl: true}} {
		if got := nextEvent(t, tm); got != want {
			t.Fatalf("event = %v, want %v", got, want)
		}
	}
	if _, err := master.WriteString("\x1b"); err != nil {
		t.Fatal(err)
	}
	if got := nextEvent(t, tm); got != (Key{Special: KeyEsc}) {
		t.Fatalf("lone ESC gave %v", got)
	}
}

func TestPTYResizeEvent(t *testing.T) {
	master, slave := openPTY(t)
	setWinsize(t, master, 80, 24)
	tm := newTerm(slave, slave)
	if err := tm.Enter(); err != nil {
		t.Fatal(err)
	}
	defer tm.Restore()
	setWinsize(t, master, 100, 30)
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	if got := nextEvent(t, tm); got != (Resize{Cols: 100, Rows: 30}) {
		t.Fatalf("event = %v, want Resize{100 30}", got)
	}
}

// The terminal goes away under the program (the master closes; the slave read fails with EIO): the event stream
// ends with Closed instead of hanging.
func TestPTYMasterClosedDeliversClosed(t *testing.T) {
	master, slave := openPTY(t)
	setWinsize(t, master, 80, 24)
	tm := newTerm(slave, slave)
	if err := tm.Enter(); err != nil {
		t.Fatal(err)
	}
	defer tm.Restore()
	master.Close()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-tm.Events():
			if e == (Closed{}) {
				return
			}
		case <-deadline:
			t.Fatal("no Closed event after the master closed")
		}
	}
}

func TestPTYWrite(t *testing.T) {
	master, slave := openPTY(t)
	setWinsize(t, master, 80, 3)
	out := startCapture(master)
	tm := newTerm(slave, slave)
	if err := tm.Enter(); err != nil {
		t.Fatal(err)
	}
	tm.Write([]string{"L1", "L2", "L3", "L4", "L5"})
	out.waitFor(t, "\x1b[J") // end of the frame
	if got := out.String(); !strings.Contains(got, "L3") || strings.Contains(got, "L4") {
		t.Fatalf("frame must be cut to 3 rows: %q", got)
	}
	tm.Restore()
	out.waitFor(t, restoreSeq)
	tm.Write([]string{"AFTER"})
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(out.String(), "AFTER") {
		t.Fatal("Write after Restore drew on the main screen")
	}
}

func TestOpenWithoutTerminal(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if _, err := open("/nonexistent/tty", null, null); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("err = %v, want ErrNoTerminal", err)
	}
}

// TestChildHelper is the body of the child processes started by the tests below; it does nothing in a
// normal run. The pty slave arrives as file descriptor 3.
func TestChildHelper(t *testing.T) {
	mode := os.Getenv("TERM_TEST_MODE")
	if mode == "" {
		return
	}
	f := os.NewFile(3, "pty")
	tm := newTerm(f, f)
	switch mode {
	case "panic":
		_ = tm.Run(func() error { panic("boom") })
	case "wait":
		if err := tm.Enter(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		fmt.Println("READY")
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func childCmd(mode string, slave *os.File) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildHelper$")
	cmd.Env = append(os.Environ(), "TERM_TEST_MODE="+mode)
	cmd.ExtraFiles = []*os.File{slave}
	return cmd
}

func exitCode(err error) int {
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	return -1
}

func TestPTYPanicRestores(t *testing.T) {
	_, slave := openPTY(t)
	orig := mustTermios(t, slave)
	out, err := childCmd("panic", slave).CombinedOutput()
	if exitCode(err) != 2 || !strings.Contains(string(out), "boom") {
		t.Fatalf("child should die by re-panic (exit 2, message boom): err=%v out=%q", err, out)
	}
	if got := mustTermios(t, slave); got != orig {
		t.Fatalf("terminal not restored after panic:\n got %+v\nwant %+v", got, orig)
	}
}

func TestPTYSignalsRestoreAndExit(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT} {
		t.Run(sig.String(), func(t *testing.T) {
			_, slave := openPTY(t)
			orig := mustTermios(t, slave)
			cmd := childCmd("wait", slave)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			ready := false
			sc := bufio.NewScanner(stdout)
			for sc.Scan() {
				if sc.Text() == "READY" {
					ready = true
					break
				}
			}
			if !ready {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("child never became ready (scan error: %v)", sc.Err())
			}
			if raw := mustTermios(t, slave); raw.Lflag&syscall.ICANON != 0 {
				t.Fatal("child did not enter raw mode")
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			if want := 128 + int(sig); exitCode(err) != want {
				t.Fatalf("exit code = %d (%v), want %d", exitCode(err), err, want)
			}
			if got := mustTermios(t, slave); got != orig {
				t.Fatalf("terminal not restored after %v:\n got %+v\nwant %+v", sig, got, orig)
			}
		})
	}
}
