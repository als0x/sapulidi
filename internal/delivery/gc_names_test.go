package delivery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/testutil"
)

func TestGCNeverRemovesNamesThatAreNotVersions(t *testing.T) {
	home := testutil.TempHome(t)
	data := filepath.Join(home, "data", "sapulidi")
	for _, n := range []string{"mydir", "v1.2.3", "1.2", "1.2.3.4", "1.2.3-", "1.2.3-rc_1", "notes"} {
		mkVersion(t, data, n, 90*day)
	}
	old := mkVersion(t, data, "1.2.3-rc.1", 90*day)
	removed, err := GC(GCOptions{Data: data, Now: time.Now()})
	if err != nil || len(removed) != 1 || removed[0] != "1.2.3-rc.1" || exists(old) {
		t.Fatalf("%v %v", removed, err)
	}
	for _, n := range []string{"mydir", "v1.2.3", "1.2", "1.2.3.4", "1.2.3-", "1.2.3-rc_1", "notes"} {
		if !exists(filepath.Join(data, "bin", n)) {
			t.Fatalf("%s was removed", n)
		}
	}
}

func TestGCUnreadableSubfolderKeepsTheVersion(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read everything")
	}
	home := testutil.TempHome(t)
	data := filepath.Join(home, "data", "sapulidi")
	dir := mkVersion(t, data, "0.1.0", 90*day)
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, sub, 90*day)
	if err := os.Chmod(sub, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })
	removed, err := GC(GCOptions{Data: data, Now: time.Now()})
	if err != nil || len(removed) != 0 || !exists(dir) {
		t.Fatalf("a walk error must count as fresh: %v %v", removed, err)
	}
}

func TestGCResolvesCurrentThroughSymlinks(t *testing.T) {
	home := testutil.TempHome(t)
	data := filepath.Join(home, "data", "sapulidi")
	cur := mkVersion(t, data, "0.1.0", 90*day)
	other := mkVersion(t, data, "0.2.0", 90*day)
	// current is an absolute link; it must still protect 0.1.0 and only that one.
	if err := os.Symlink(cur, filepath.Join(data, "bin", "current")); err != nil {
		t.Fatal(err)
	}
	removed, err := GC(GCOptions{Data: data, Now: time.Now()})
	if err != nil || len(removed) != 1 || removed[0] != "0.2.0" || !exists(cur) || exists(other) {
		t.Fatalf("%v %v", removed, err)
	}
}
