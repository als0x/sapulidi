package cli

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// swapStdio points os.Stdin and os.Stdout at in and out for the length of the test.
func swapStdio(t *testing.T, in, out *os.File) {
	t.Helper()
	oi, oo := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = in, out
	t.Cleanup(func() { os.Stdin, os.Stdout = oi, oo })
}

func TestRealIsTerminalFalseOnDevNull(t *testing.T) {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	swapStdio(t, null, null)
	if isTerminal(&App{Stdout: os.Stdout}) {
		t.Error("/dev/null is a character device but not a terminal")
	}
	if fdIsTTY(null.Fd()) {
		t.Error("fdIsTTY true for /dev/null")
	}
}

func TestRealIsTerminalNeedsProcessStdout(t *testing.T) {
	if isTerminal(&App{Stdout: &bytes.Buffer{}}) {
		t.Error("a buffer is not the process stdout")
	}
}

func TestRealIsTerminalTrueOnPty(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pty slave is opened through linux ioctls only; macOS skipped")
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	defer master.Close()
	var unlock int32
	const tiocsptlck, tiocgptn = 0x40045431, 0x80045430
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocsptlck, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlock pty: %v", e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocgptn, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Skipf("pty number: %v", e)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	defer slave.Close()
	swapStdio(t, slave, slave)
	if !isTerminal(&App{Stdout: os.Stdout}) {
		t.Error("pty slave should be a terminal")
	}
}
