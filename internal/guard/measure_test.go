package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
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

	m, err := MeasureWithRepo(context.Background(), dir)
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
	m, err := MeasureWithRepo(context.Background(), root)
	if err != nil || m.HasRepo {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestMeasureWithRepoBareRepository(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "mirror", "HEAD"), "ref: x")
	write(t, filepath.Join(root, "mirror", "objects", "pack", "p"), "x")
	m, err := MeasureWithRepo(context.Background(), root)
	if err != nil || !m.HasRepo {
		t.Fatalf("a bare repository must set HasRepo: %+v %v", m, err)
	}
}

func TestMeasurePlainSkipsRepoCheck(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", ".git", "HEAD"), "ref: x")
	m, err := Measure(context.Background(), root)
	if err != nil || m.HasRepo {
		t.Fatalf("plain Measure must not look for repositories: %+v %v", m, err)
	}
}

func TestMeasureBorn(t *testing.T) {
	root := t.TempDir()
	m, err := Measure(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if m.Born.IsZero() {
		t.Skip("this filesystem reports no birth time")
	}
	if d := time.Since(m.Born); d < -5*time.Second || d > 10*time.Second {
		t.Fatalf("born %v (%v ago)", m.Born, d)
	}
}

// cancelAfter reports context.Canceled once Err has been asked n times.
type cancelAfter struct {
	context.Context
	n int
}

func (c *cancelAfter) Err() error {
	if c.n--; c.n < 0 {
		return context.Canceled
	}
	return nil
}

func TestMeasureCancelMidWalkReturnsPartial(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		write(t, filepath.Join(root, "d", fmt.Sprintf("f%02d", i)), "data")
	}
	full, err := Measure(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Measure(&cancelAfter{Context: context.Background(), n: 5}, root)
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if m.Files == 0 || m.Files >= full.Files || m.Bytes == 0 || m.Bytes >= full.Bytes {
		t.Fatalf("want partial result, got %+v of %+v", m, full)
	}
}

// Measure must not do the per-directory repository Lstats the engine never reads: count them through a tree of
// 5000 directories by checking the plain walk leaves HasRepo unset while one holds a .git.
func TestMeasureManyDirectories(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5000; i++ {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("d%04d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "d0042", ".git", "HEAD"), "x")
	m, err := Measure(context.Background(), root)
	if err != nil || m.HasRepo {
		t.Fatalf("%+v %v", m, err)
	}
	if r, _ := MeasureWithRepo(context.Background(), root); !r.HasRepo {
		t.Fatal("MeasureWithRepo must find the repository")
	}
}

func BenchmarkMeasure5000Dirs(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 5000; i++ {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("d%04d", i)), 0o755); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Measure(context.Background(), root); err != nil {
			b.Fatal(err)
		}
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
