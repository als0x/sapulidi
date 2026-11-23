package sizing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDirPartialWhenUnreadable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), make([]byte, 8192), 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	n, known := Dir(context.Background(), root)
	if n <= 0 {
		t.Errorf("partial sum lost: %d", n)
	}
	if known {
		t.Error("an unreadable directory must make the size unknown")
	}
	if n, known := Dir(context.Background(), filepath.Join(root, "missing")); n != 0 || known {
		t.Errorf("missing path: %d %v", n, known)
	}
}

func TestWithinAbandonsBlockedCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	block := make(chan struct{})
	defer close(block)
	start := time.Now()
	if Within(ctx, func() { <-block }) {
		t.Error("a blocked call must not report finished")
	}
	if time.Since(start) > time.Second {
		t.Error("deadline not honoured")
	}
	if !Within(context.Background(), func() {}) {
		t.Error("a quick call finishes")
	}
}
