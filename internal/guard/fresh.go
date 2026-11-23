package guard

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

func useCtime() bool {
	return !(os.Getenv("SAPULIDI_TEST") == "1" && os.Getenv("SAPULIDI_TEST_NO_CTIME") == "1")
}

// Newest returns the newest mtime and (outside the test flag) ctime of path and everything under it,
// without following symlinks.
func Newest(path string) (time.Time, error) {
	ctime := useCtime()
	var newest time.Time
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if t := fi.ModTime(); t.After(newest) {
			newest = t
		}
		if ctime {
			if t, ok := ctimeOf(fi); ok && t.After(newest) {
				newest = t
			}
		}
		return nil
	})
	return newest, err
}

// Fresh keeps path as "young" when something inside changed less than age ago.
func Fresh(path string, age time.Duration, now time.Time) error {
	n, err := Newest(path)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	if now.Sub(n) < age {
		return core.Keep("young", fmt.Sprintf("changed %s ago", now.Sub(n).Round(time.Second)))
	}
	return nil
}
