//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const (
	tcgets     = 0x5401
	tiocgptn   = 0x80045430
	tiocsptlck = 0x40045431
	tiocswinsz = 0x5414
)

func ptyIoctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	var unlock int32
	if err := ptyIoctl(m.Fd(), tiocsptlck, unsafe.Pointer(&unlock)); err != nil {
		t.Fatalf("TIOCSPTLCK: %v", err)
	}
	var n uint32
	if err := ptyIoctl(m.Fd(), tiocgptn, unsafe.Pointer(&n)); err != nil {
		t.Fatalf("TIOCGPTN: %v", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}

// TestTUIQuitsAndLeavesCookedMode runs the built binary in a temp HOME on a pseudo-terminal, waits for the list and
// quits with q; the terminal must be back in cooked mode (ICANON and ECHO set).
func TestTUIQuitsAndLeavesCookedMode(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	bin := filepath.Join(t.TempDir(), "sapulidi")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", bin, "./cmd/sapulidi")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	master, slave := openPTY(t)
	ws := struct{ Row, Col, X, Y uint16 }{Row: 30, Col: 100}
	if err := ptyIoctl(master.Fd(), tiocswinsz, unsafe.Pointer(&ws)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var screen strings.Builder
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			screen.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	seen := func(sub string) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(screen.String(), sub)
	}

	cmd := exec.Command(bin)
	cmd.Env = []string{"HOME=" + home, "TMPDIR=" + home, "XDG_CONFIG_HOME=" + home + "/c", "XDG_DATA_HOME=" + home + "/d",
		"XDG_STATE_HOME=" + home + "/s", "PATH=" + os.Getenv("PATH"), "TERM=xterm"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	deadline := time.Now().Add(60 * time.Second)
	for !seen("summary") {
		if time.Now().After(deadline) {
			t.Fatalf("the list never appeared; output:\n%q", screen.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := master.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the program did not quit on q")
	}
	var tio syscall.Termios
	if err := ptyIoctl(slave.Fd(), tcgets, unsafe.Pointer(&tio)); err != nil {
		t.Fatal(err)
	}
	if tio.Lflag&syscall.ICANON == 0 || tio.Lflag&syscall.ECHO == 0 {
		t.Fatalf("terminal left raw: lflag %#x", tio.Lflag)
	}
}
