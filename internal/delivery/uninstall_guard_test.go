package delivery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/testutil"
)

func TestGuardRefusesDangerousOverrides(t *testing.T) {
	home := testutil.TempHome(t)
	for _, d := range []string{"Development", "Documents"} {
		if err := os.MkdirAll(filepath.Join(home, d, "bin", "1.0.0"), 0o755); err != nil {
			t.Fatal(err)
		}
		testutil.Touch(t, filepath.Join(home, d, "bin", "1.0.0"), 90*day)
		if err := os.WriteFile(filepath.Join(home, d, "keep.txt"), []byte("mine"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bad := map[string]string{
		"home":           home,
		"parent of home": filepath.Dir(home),
		"Development":    filepath.Join(home, "Development"),
		"Documents":      filepath.Join(home, "Documents"),
		"usr-local":      "/usr/local",
		"etc":            "/etc",
		"root":           "/",
		"relative":       "sapulidi",
		"misnamed":       filepath.Join(home, "stuff"),
		"dotdot":         filepath.Join(home, "x", "..", "Documents"),
	}
	for name, dir := range bad {
		for _, which := range []string{"data", "state", "config"} {
			for _, all := range []bool{false, true} {
				p := paths(home)
				seed(t, p)
				switch which {
				case "data":
					p.Data = dir
				case "state":
					p.State = dir
				case "config":
					p.Config = filepath.Join(dir, "config.toml")
				}
				be := &fakeBackend{}
				res, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: be, All: all})
				if err == nil || len(be.removed) != 0 || len(res.Removed) != 0 {
					t.Fatalf("%s as %s (all=%v): err %v, schedule %v, removed %v", name, which, all, err, be.removed, res.Removed)
				}
				for _, d := range []string{"Development", "Documents"} {
					if !exists(filepath.Join(home, d, "keep.txt")) || !exists(filepath.Join(home, d, "bin", "1.0.0")) {
						t.Fatalf("%s as %s: %s was touched", name, which, d)
					}
				}
				if !exists(filepath.Join(paths(home).Data, "bin", "0.1.0")) || !exists(paths(home).State) {
					t.Fatalf("%s as %s: sapulidi's own directories were touched", name, which)
				}
			}
		}
		// GC refuses the same data directories and removes nothing.
		removed, err := GC(GCOptions{Data: dir, Home: home, Now: time.Now()})
		if err == nil || len(removed) != 0 || !exists(filepath.Join(home, "Documents", "bin", "1.0.0")) {
			t.Fatalf("GC with data %s: %v %v", name, removed, err)
		}
	}
}

func TestGuardRefusesNestedDirectories(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	p.Data = filepath.Join(p.State, "sapulidi") // data inside state
	if _, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: &fakeBackend{}}); err == nil {
		t.Fatal("data inside state must be refused")
	}
	p = paths(home)
	p.State = p.Data // the same directory twice
	if _, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: &fakeBackend{}}); err == nil {
		t.Fatal("state equal to data must be refused")
	}
	if !exists(filepath.Join(paths(home).Data, "bin", "0.1.0")) {
		t.Fatal("binaries were touched")
	}
}

func TestUninstallKeepsUnknownEntriesInBin(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	bin := filepath.Join(p.Data, "bin")
	mkVersion(t, p.Data, "1.2.3-rc.1", day)
	mkVersion(t, p.Data, "v2", day) // not a version name
	for _, d := range []string{"mydir", ".lock"} {
		if err := os.MkdirAll(filepath.Join(bin, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, ".download.abc"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("0.1.0", filepath.Join(bin, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("0.1.0", filepath.Join(bin, ".current.123")); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: &fakeBackend{}}); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"0.1.0", "1.2.3-rc.1", "current", ".current.123", ".lock", ".download.abc"} {
		if exists(filepath.Join(bin, gone)) {
			t.Fatalf("%s should have been removed", gone)
		}
	}
	for _, kept := range []string{"v2", "mydir", "notes.txt"} {
		if !exists(filepath.Join(bin, kept)) {
			t.Fatalf("%s is not sapulidi's and must stay", kept)
		}
	}
}
