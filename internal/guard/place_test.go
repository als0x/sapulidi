package guard

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/als0x/sapulidi/internal/testutil"
)

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPlaceAcceptsChild(t *testing.T) {
	base := realDir(t)
	write(t, filepath.Join(base, "a", "b", "f"), "x")
	wantWhy(t, Place(&testutil.FakePlatform{}, base, filepath.Join(base, "a", "b")), "")
}

func TestPlaceRejectsSymlinkComponent(t *testing.T) {
	base := realDir(t)
	other := realDir(t)
	write(t, filepath.Join(other, "inner", "f"), "x")
	if err := os.Symlink(other, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	p := &testutil.FakePlatform{}
	wantWhy(t, Place(p, base, filepath.Join(base, "link", "inner")), "place")
	wantWhy(t, Place(p, base, filepath.Join(base, "link")), "place")
	// base itself a symlink
	lb := filepath.Join(realDir(t), "lb")
	if err := os.Symlink(base, lb); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(base, "c", "f"), "x")
	wantWhy(t, Place(p, lb, filepath.Join(lb, "c")), "place")
}

func TestPlaceRejectsDotDot(t *testing.T) {
	base := realDir(t)
	write(t, filepath.Join(base, "a", "f"), "x")
	write(t, filepath.Join(base, "b", "f"), "x")
	p := &testutil.FakePlatform{}
	wantWhy(t, Place(p, base, base+"/a/../b"), "place")
	wantWhy(t, Place(p, base, base), "place")
	wantWhy(t, Place(p, base, filepath.Dir(base)), "place")
	wantWhy(t, Place(p, base, base+"/a/"), "place")
	wantWhy(t, Place(p, "relative/base", "relative/base/a"), "place")
}

func TestPlaceRejectsRootBase(t *testing.T) {
	wantWhy(t, Place(&testutil.FakePlatform{}, "/", "/tmp"), "place")
}

func TestPlaceRejectsOtherDevice(t *testing.T) {
	base := realDir(t)
	child := filepath.Join(base, "a")
	write(t, filepath.Join(child, "f"), "x")
	p := &testutil.FakePlatform{OtherDevice: map[string]bool{child: true}}
	wantWhy(t, Place(p, base, child), "place")
}

func TestPlaceRejectsSocket(t *testing.T) {
	// macOS limits socket paths to 104 bytes: use a short directory.
	dir, err := os.MkdirTemp("/tmp", "g")
	if err != nil {
		t.Skip("no /tmp")
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	base, _ := filepath.EvalSymlinks(dir)
	sock := filepath.Join(base, "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	wantWhy(t, Place(&testutil.FakePlatform{}, base, sock), "place")
}
