package engine

import (
	"context"
	"io/fs"
	"path/filepath"
	"syscall"
)

// Size is the allocated bytes under path, without following symlinks; -1 on error.
func Size(path string) int64 { return sizeCtx(context.Background(), path) }

func sizeCtx(ctx context.Context, path string) int64 {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
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
			total += int64(st.Blocks) * 512
		} else {
			total += fi.Size()
		}
		return nil
	})
	if err != nil {
		return -1
	}
	return total
}
