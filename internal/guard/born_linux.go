package guard

import (
	"syscall"
	"time"
	"unsafe"
)

const (
	atFdcwd           = -100  // AT_FDCWD
	atSymlinkNofollow = 0x100 // AT_SYMLINK_NOFOLLOW
	statxBtime        = 0x800 // STATX_BTIME
)

type statxTimestamp struct {
	Sec  int64
	Nsec uint32
	_    int32
}

// statxT is struct statx (256 bytes).
type statxT struct {
	Mask           uint32
	Blksize        uint32
	Attributes     uint64
	Nlink          uint32
	UID            uint32
	GID            uint32
	Mode           uint16
	_              uint16
	Ino            uint64
	Size           uint64
	Blocks         uint64
	AttributesMask uint64
	Atime          statxTimestamp
	Btime          statxTimestamp
	Ctime          statxTimestamp
	Mtime          statxTimestamp
	_              [128]byte
}

// bornOf is the birth time of path itself, not following a symlink, through statx; zero when the kernel or the
// filesystem gives none.
func bornOf(path string) time.Time {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return time.Time{}
	}
	var sx statxT
	dirfd := atFdcwd
	_, _, errno := syscall.Syscall6(sysStatx, uintptr(dirfd), uintptr(unsafe.Pointer(p)),
		atSymlinkNofollow, statxBtime, uintptr(unsafe.Pointer(&sx)), 0)
	if errno != 0 || sx.Mask&statxBtime == 0 {
		return time.Time{}
	}
	if sx.Btime.Sec == 0 && sx.Btime.Nsec == 0 {
		return time.Time{}
	}
	return time.Unix(sx.Btime.Sec, int64(sx.Btime.Nsec))
}
