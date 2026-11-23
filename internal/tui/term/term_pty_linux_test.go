//go:build linux

package term

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	orig := mustTermios(t, slave)
	tm := newTerm(slave, slave)

	if err := tm.Enter(); err != nil {
		t.Fatal(err)
	}
	raw := mustTermios(t, slave)
	if raw.Lflag&(syscall.ICANON|syscall.ECHO|syscall.ISIG) != 0 {
		t.Fatalf("slave is not raw after Enter: lflag=%#x", raw.Lflag)
	}
	tm.Restore()
	tm.Restore() // idempotent
	if got := mustTermios(t, slave); got != orig {
		t.Fatalf("termios after Restore differs from the original:\n got %+v\nwant %+v", got, orig)
	}

	if master.SetReadDeadline(time.Now().Add(time.Second)) == nil {
		var seen strings.Builder
		buf := make([]byte, 256)
		for !strings.Contains(seen.String(), restoreSeq) {
			n, err := master.Read(buf)
			seen.Write(buf[:n])
			if err != nil {
				break
			}
		}
		if !strings.Contains(seen.String(), enterSeq) || !strings.Contains(seen.String(), restoreSeq) {
			t.Fatalf("screen sequences missing from output %q", seen.String())
		}
	}
}

func TestPTYKeysAndSize(t *testing.T) {
	master, slave := openPTY(t)
	ws := struct{ Row, Col, X, Y uint16 }{Row: 24, Col: 80}
	if err := ioctl(master.Fd(), tiocswinsz, unsafe.Pointer(&ws)); err != nil {
		t.Fatal(err)
	}
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
	case "hup":
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
	var ee *exec.ExitError
	if errors.As(err, &ee) {
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

func TestPTYSighupRestores(t *testing.T) {
	_, slave := openPTY(t)
	orig := mustTermios(t, slave)
	cmd := childCmd("hup", slave)
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
		t.Fatal("child never became ready")
	}
	if raw := mustTermios(t, slave); raw.Lflag&syscall.ICANON != 0 {
		t.Fatal("child did not enter raw mode")
	}
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	if exitCode(err) != 128+int(syscall.SIGHUP) {
		t.Fatalf("exit code = %d (%v), want %d", exitCode(err), err, 128+int(syscall.SIGHUP))
	}
	if got := mustTermios(t, slave); got != orig {
		t.Fatalf("terminal not restored after SIGHUP:\n got %+v\nwant %+v", got, orig)
	}
}
