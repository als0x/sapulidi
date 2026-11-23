package exclude

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLinkInsideExcludedFolderIsKept(t *testing.T) {
	l, _ := Load(tempPaths(t))
	root := mkdir(t, t.TempDir())
	keep := mkdir(t, filepath.Join(root, "keep"))
	other := mkdir(t, filepath.Join(root, "elsewhere", "cache"))
	link := filepath.Join(keep, "link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	l.Add(keep, "", day)
	for _, p := range []string{link, filepath.Join(link, "sub")} {
		if _, ok := l.Match(p); !ok {
			t.Errorf("Match(%s) = false", p)
		}
		if _, ok := l.Within(p); !ok {
			t.Errorf("Within(%s) = false", p)
		}
	}
	if _, ok := l.Match(other); ok {
		t.Error("the link target itself must not be excluded")
	}
}

func TestHandWrittenEntriesStillProtect(t *testing.T) {
	p := tempPaths(t)
	root := mkdir(t, t.TempDir())
	real := mkdir(t, filepath.Join(root, "real", "x"))
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Dir(real), alias); err != nil {
		t.Fatal(err)
	}
	b := mkdir(t, filepath.Join(root, "b"))
	toml := fmt.Sprintf("[[exclude]]\npath = %q\nadded = \"2026-01-01\"\n[[exclude]]\npath = %q\nadded = \"2026-01-01\"\n[[exclude]]\npath = %q\nadded = \"2026-01-01\"\n",
		filepath.Join(alias, "x"), root+"/a/../b", root+"//c")
	os.MkdirAll(filepath.Dir(p.Config), 0o700)
	if err := os.WriteFile(filepath.Join(filepath.Dir(p.Config), FileName), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{real, filepath.Join(alias, "x", "deep"), filepath.Join(real, "deep"), b, filepath.Join(b, "z"), root + "/c", root + "/c/z"} {
		if _, ok := l.Match(c); !ok {
			t.Errorf("Match(%s) = false", c)
		}
	}
	if added, _, _ := l.Add(real, "", day); added {
		t.Error("Add of an already hand-excluded path must be a no-op")
	}
	if err := l.Remove(b); err != nil {
		t.Errorf("Remove of a hand-written entry by its real path: %v", err)
	}
}

func TestPrefixTrap(t *testing.T) {
	l, _ := Load(tempPaths(t))
	root := mkdir(t, t.TempDir())
	l.Add(filepath.Join(root, "a", "b"), "", day)
	for _, p := range []string{filepath.Join(root, "a", "bc"), filepath.Join(root, "a", "bc", "d"), filepath.Join(root, "a", "b2")} {
		if m, ok := l.Match(p); ok {
			t.Errorf("Match(%s) matched %s", p, m)
		}
	}
}

func TestMatchIgnoresCase(t *testing.T) {
	l, _ := Load(tempPaths(t))
	root := mkdir(t, t.TempDir())
	l.Add(filepath.Join(root, "Keep"), "", day)
	if _, ok := l.Match(filepath.Join(root, "KEEP", "x")); !ok {
		t.Error("case-insensitive match expected: it keeps more, never less")
	}
}

func TestUpdateKeepsBothWriters(t *testing.T) {
	p := tempPaths(t)
	root := mkdir(t, t.TempDir())
	a, b := mkdir(t, filepath.Join(root, "a")), mkdir(t, filepath.Join(root, "b"))
	l1, _ := Load(p)
	l2, _ := Load(p) // stale: loaded before l1 saves
	l1.Add(a, "", day)
	if err := l1.Save(); err != nil {
		t.Fatal(err)
	}
	_ = l2
	if err := Update(p, func(l *List) error { l.Add(b, "", day); return l.Save() }); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(p)
	if len(got.Entries()) != 2 {
		t.Fatalf("lost an update: %+v", got.Entries())
	}
}

func TestUpdateConcurrent(t *testing.T) {
	p := tempPaths(t)
	root := mkdir(t, t.TempDir())
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		d := mkdir(t, filepath.Join(root, fmt.Sprintf("d%d", i)))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Update(p, func(l *List) error { l.Add(d, "", day); return l.Save() }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, _ := Load(p)
	if len(got.Entries()) != n {
		t.Fatalf("%d entries, want %d", len(got.Entries()), n)
	}
}
