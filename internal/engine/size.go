package engine

import (
	"context"

	"github.com/als0x/sapulidi/internal/guard"
)

// Size is the allocated bytes under path, without following symlinks; -1 on error.
func Size(path string) int64 { return sizeCtx(context.Background(), path) }

func sizeCtx(ctx context.Context, path string) int64 {
	m, err := guard.Measure(ctx, path)
	if err != nil {
		return -1
	}
	return m.Bytes
}
