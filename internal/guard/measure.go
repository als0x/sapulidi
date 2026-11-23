package guard

import (
	"context"
	"io/fs"
	"path/filepath"
	"syscall"
	"time"
)

// Measurement is everything one walk of a path tells.
type Measurement struct {
	Bytes   int64     // allocated bytes, like the engine size walk
	Files   int       // entries that are not directories
	Newest  time.Time // the value Newest returns
	Born    time.Time // birth time of the path itself; zero when unknown
	HasRepo bool      // only set by MeasureWithRepo: a .git entry or a bare repository inside (the HoldsRepo rule)
}

// Measure walks path once, without following symlinks. A walk error is returned with what was gathered so far;
// a cancelled ctx returns ctx.Err() the same way. It does not look for repositories (that costs two extra
// Lstat per directory); MeasureWithRepo does.
func Measure(ctx context.Context, path string) (Measurement, error) {
	return measure(ctx, path, false)
}

// MeasureWithRepo is Measure that also sets HasRepo.
func MeasureWithRepo(ctx context.Context, path string) (Measurement, error) {
	return measure(ctx, path, true)
}

func measure(ctx context.Context, path string, repo bool) (Measurement, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m := Measurement{Born: bornOf(path)}
	ctime := useCtime()
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			m.Bytes += int64(st.Blocks) * 512
		} else {
			m.Bytes += fi.Size()
		}
		if !d.IsDir() {
			m.Files++
		}
		if t := fi.ModTime(); t.After(m.Newest) {
			m.Newest = t
		}
		if ctime {
			if t, ok := ctimeOf(fi); ok && t.After(m.Newest) {
				m.Newest = t
			}
		}
		if repo && !m.HasRepo && (d.Name() == ".git" || (d.IsDir() && isBareRepo(p))) {
			m.HasRepo = true
		}
		return nil
	})
	return m, err
}
