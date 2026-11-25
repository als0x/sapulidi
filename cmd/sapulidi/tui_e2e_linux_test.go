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

// TestTUIQuitsAndLeavesCookedMode runs the built binary in a temp HOME on a pseudo-terminal, waits for the places
// list, opens ~/tmp/ to verify the preselected leftover folder, deletes it with d and y, and quits; the folder must
// be gone and the terminal back in cooked mode (ICANON and ECHO set).
func TestTUIQuitsAndLeavesCookedMode(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	// Everything the scan reads is inside home: the temp dir and the agent scratch dir come from the test hooks.
	tmp := filepath.Join(home, "tmp")
	victim := filepath.Join(tmp, "go-build123")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "a.o"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{filepath.Join(victim, "a.o"), victim} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	scratch := filepath.Join(home, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
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
	cmd.Env = []string{"HOME=" + home, "TMPDIR=" + tmp, "XDG_CONFIG_HOME=" + home + "/c", "XDG_DATA_HOME=" + home + "/d",
		"XDG_STATE_HOME=" + home + "/s", "PATH=" + os.Getenv("PATH"), "TERM=xterm",
		"SAPULIDI_TEST=1", "SAPULIDI_TEST_TEMPDIR=" + tmp, "SAPULIDI_TEST_SCRATCH=" + scratch, "SAPULIDI_TEST_NO_CTIME=1"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	waitSeen := func(sub string) {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for !seen(sub) {
			if time.Now().After(deadline) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("%q never appeared; output:\n%q", sub, screen.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	send := func(keys string) {
		t.Helper()
		if _, err := master.Write([]byte(keys)); err != nil {
			t.Fatal(err)
		}
	}
	waitSeen("~/tmp/")
	send("\r") // open the place to show the preselected row inside
	waitSeen("go-build123")
	send("d")
	waitSeen("Delete 1 item")
	send("y")
	waitSeen("removed 1 items")
	send("q") // result screen back to the list
	send("q") // quit
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatalf("the folder is still there: %v", err)
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
