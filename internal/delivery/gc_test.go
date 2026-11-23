package delivery

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/testutil"
)

const day = 24 * time.Hour

// mkVersion creates <data>/bin/<v>/sapulidi whose mtime is age old (the directory too).
func mkVersion(t *testing.T, data, v string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(data, "bin", v)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "sapulidi")
	if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, f, age)
	testutil.Touch(t, dir, age)
	return dir
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestGCKeepsCurrentRunningAndHeartbeatVersions(t *testing.T) {
	home := testutil.TempHome(t)
	data := filepath.Join(home, "data", "sapulidi")
	cur := mkVersion(t, data, "0.1.0", 90*day)
	run := mkVersion(t, data, "0.2.0", 90*day)
	named := mkVersion(t, data, "0.3.0", 90*day)
	young := mkVersion(t, data, "0.4.0", 2*day)
	if err := os.Symlink("0.1.0", filepath.Join(data, "bin", "current")); err != nil {
		t.Fatal(err)
	}
	removed, err := GC(GCOptions{
		Data: data, Now: time.Now(), Named: []string{"0.3.0"},
		Running: filepath.Join(run, "sapulidi"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v", removed)
	}
	for _, d := range []string{cur, run, named, young} {
		if !exists(d) {
			t.Fatalf("%s was removed", d)
		}
	}
	if !exists(filepath.Join(data, "bin", "current")) {
		t.Fatal("current link removed")
	}
}

func TestGCRemovesOldUnnamed(t *testing.T) {
	home := testutil.TempHome(t)
	data := filepath.Join(home, "data", "sapulidi")
	mkVersion(t, data, "0.9.0", 1*day)
	old := mkVersion(t, data, "0.1.0", 90*day)
	// A directory whose directory mtime is old but holds a recent file is not old.
	mixed := mkVersion(t, data, "0.2.0", 90*day)
	testutil.Touch(t, filepath.Join(mixed, "sapulidi"), 1*day)
	removed, err := GC(GCOptions{Data: data, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(removed)
	if len(removed) != 1 || removed[0] != "0.1.0" || exists(old) || !exists(mixed) {
		t.Fatalf("removed %v", removed)
	}
}

func TestGCIgnoresOddEntries(t *testing.T) {
	home := testutil.TempHome(t)
	data := filepath.Join(home, "data", "sapulidi")
	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, outside, 90*day)
	bin := filepath.Join(data, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(bin, "9.9.9")); err != nil { // a symlink is not a version dir
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "stray"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if removed, err := GC(GCOptions{Data: data, Now: time.Now()}); err != nil || len(removed) != 0 {
		t.Fatalf("%v %v", removed, err)
	}
	if !exists(outside) || !exists(filepath.Join(bin, "stray")) {
		t.Fatal("GC touched something that is not a version directory")
	}
}

func TestGCRunsAtMostDaily(t *testing.T) {
	home := testutil.TempHome(t)
	data := filepath.Join(home, "data", "sapulidi")
	state := filepath.Join(home, "state", "sapulidi")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	mkVersion(t, data, "0.1.0", 90*day)
	opt := GCOptions{Data: data, Now: time.Now()}
	removed, ran, err := GCDaily(state, opt)
	if err != nil || !ran || len(removed) != 1 {
		t.Fatalf("first: %v %v %v", removed, ran, err)
	}
	mkVersion(t, data, "0.0.5", 90*day)
	removed, ran, err = GCDaily(state, opt)
	if err != nil || ran || len(removed) != 0 || !exists(filepath.Join(data, "bin", "0.0.5")) {
		t.Fatalf("second run inside a day: %v %v %v", removed, ran, err)
	}
	opt.Now = time.Now().Add(25 * time.Hour)
	removed, ran, err = GCDaily(state, opt)
	if err != nil || !ran || len(removed) != 1 {
		t.Fatalf("after a day: %v %v %v", removed, ran, err)
	}
}
