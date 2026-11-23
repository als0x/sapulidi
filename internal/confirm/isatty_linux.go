package confirm

import (
	"syscall"
	"unsafe"
)

// isTerminal asks the kernel for the terminal attributes of fd; only a terminal answers.
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}
