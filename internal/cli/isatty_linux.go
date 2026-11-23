package cli

import (
	"syscall"
	"unsafe"
)

// fdIsTTY asks the kernel for the terminal attributes of fd; only a terminal answers.
func fdIsTTY(fd uintptr) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}
