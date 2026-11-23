// Package sizing measures directories for the report without trusting the disk: unreadable entries are skipped,
// the partial sum is kept, and the caller is told when the answer is not complete.
package sizing

import (
	"context"
	"io/fs"
	"path/filepath"
	"syscall"
)

// Parent is ctx, or a background context when ctx is nil.
func Parent(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// Allocated is the allocated bytes of one entry.
func Allocated(fi fs.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}

// Dir is the allocated bytes under path without following symlinks. known is false when an entry could not be
// read or ctx ended first; the sum is then partial. It is never negative.
func Dir(ctx context.Context, path string) (n int64, known bool) {
	known = true
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if e := ctx.Err(); e != nil {
			known = false
			return e
		}
		if err != nil {
			known = false
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			known = false
			return nil
		}
		n += Allocated(fi)
		return nil
	})
	return n, known
}

// Within runs fn and returns true when it finished before ctx ended. A call blocked in the kernel cannot be
// cancelled, so fn keeps running in the background after false; the caller must not read what fn writes.
func Within(ctx context.Context, fn func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
