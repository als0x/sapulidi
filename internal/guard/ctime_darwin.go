package guard

import (
	"os"
	"syscall"
	"time"
)

func ctimeOf(fi os.FileInfo) (time.Time, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(int64(st.Ctimespec.Sec), int64(st.Ctimespec.Nsec)), true
}
