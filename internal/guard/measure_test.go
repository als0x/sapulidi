package guard

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMeasureMatchesWalks(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "one.bin"), "hello")
	write(t, filepath.Join(root, "a", "b", "two.bin"), "world!!")
	write(t, filepath.Join(root, "a", ".git", "HEAD"), "ref: x")
	if err := os.Symlink("/", filepath.Join(root, "a", "link")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "a")

	m, err := Measure(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var wantBytes int64
	files := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		fi, _ := d.Info()
		wantBytes += int64(fi.Sys().(*syscall.Stat_t).Blocks) * 512
		if !d.IsDir() {
			files++
		}
		return nil
	})
	if m.Bytes != wantBytes {
		t.Fatalf("bytes %d want %d", m.Bytes, wantBytes)
	}
	if m.Files != files || files != 4 {
		t.Fatalf("files %d want %d (4 expected: 3 files + the symlink)", m.Files, files)
	}
	n, err := Newest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Newest.Equal(n) {
		t.Fatalf("newest %v want %v", m.Newest, n)
	}
	if !m.HasRepo {
		t.Fatal("a .git inside must set HasRepo")
	}
}

func TestMeasureNoRepo(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "x.txt"), "x")
	m, err := Measure(context.Background(), root)
	if err != nil || m.HasRepo {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestMeasureCancelled(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "x.txt"), "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Measure(ctx, root)
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestMeasureMissingPath(t *testing.T) {
	if _, err := Measure(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("a missing path must be an error")
	}
}
