package guard

import (
	"syscall"
	"time"
)

// bornOf is the birth time (st_birthtime) of path itself, not following a symlink; zero when unknown.
func bornOf(path string) time.Time {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return time.Time{}
	}
	if st.Birthtimespec.Sec == 0 && st.Birthtimespec.Nsec == 0 {
		return time.Time{}
	}
	return time.Unix(int64(st.Birthtimespec.Sec), int64(st.Birthtimespec.Nsec))
}
