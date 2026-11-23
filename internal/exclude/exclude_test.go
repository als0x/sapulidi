package exclude

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/testutil"
)

var day = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

func tempPaths(t *testing.T) config.Paths {
	t.Helper()
	home := testutil.TempHome(t)
	return config.Paths{Config: filepath.Join(home, "cfg", "sapulidi", "config.toml")}
}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRoundTripAddListRemove(t *testing.T) {
	p := tempPaths(t)
	l, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != 0 {
		t.Fatal("missing file must be an empty list")
	}
	dir := mkdir(t, filepath.Join(t.TempDir(), "proj"))
	added, _, err := l.Add(dir, "mine", day)
	if err != nil || !added {
		t.Fatalf("add: %v %v", added, err)
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	l2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	es := l2.Entries()
	if len(es) != 1 || es[0].Path != dir || es[0].Added != "2026-11-24" || es[0].Note != "mine" {
		t.Fatalf("entries %+v", es)
	}
	if err := l2.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := l2.Save(); err != nil {
		t.Fatal(err)
	}
	l3, _ := Load(p)
	if len(l3.Entries()) != 0 {
		t.Fatalf("not removed: %+v", l3.Entries())
	}
}

func TestSaveModeAndNoTempFile(t *testing.T) {
	p := tempPaths(t)
	l, _ := Load(p)
	l.Add(mkdir(t, filepath.Join(t.TempDir(), "a")), "", day)
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(p.Config), "exclude.toml")
	fi, err := os.Stat(file)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v %v", fi, err)
	}
	di, _ := os.Stat(filepath.Dir(file))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", di.Mode().Perm())
	}
	ents, _ := os.ReadDir(filepath.Dir(file))
	for _, e := range ents {
		if e.Name() != "exclude.toml" {
			t.Fatalf("stray file %s", e.Name())
		}
	}
}

func TestAddTwiceIsNoop(t *testing.T) {
	l, _ := Load(tempPaths(t))
	dir := mkdir(t, filepath.Join(t.TempDir(), "a"))
	l.Add(dir, "", day)
	added, by, err := l.Add(filepath.Join(dir, "sub"), "", day)
	if err != nil || added || by != dir {
		t.Fatalf("added=%v by=%q err=%v", added, by, err)
	}
	added, _, _ = l.Add(dir+"/", "", day)
	if added || len(l.Entries()) != 1 {
		t.Fatalf("duplicate stored: %+v", l.Entries())
	}
}

func TestRemoveUnknownIsError(t *testing.T) {
	l, _ := Load(tempPaths(t))
	if err := l.Remove("/no/such/entry"); err == nil {
		t.Fatal("want error")
	}
}

func TestMatchTable(t *testing.T) {
	l, _ := Load(tempPaths(t))
	root := mkdir(t, t.TempDir())
	proj := mkdir(t, filepath.Join(root, "proj"))
	nm := mkdir(t, filepath.Join(proj, "node_modules"))
	keep := mkdir(t, filepath.Join(nm, "keep-me"))
	other := mkdir(t, filepath.Join(root, "other"))
	l.Add(keep+"/", "", day) // trailing slash
	l.Add(filepath.Join(root, "g*", "cache"), "", day)
	mkdir(t, filepath.Join(root, "gone", "cache", "deep"))
	ghost := filepath.Join(root, "ghost", "x") // does not exist
	l.Add(ghost, "", day)
	link := filepath.Join(root, "link")
	if err := os.Symlink(keep, link); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, path string
		want       bool
	}{
		{"exact", keep, true},
		{"below", filepath.Join(keep, "a", "b"), true},
		{"contains an exclusion", nm, true},
		{"contains, higher up", proj, true},
		{"sibling", filepath.Join(nm, "other"), false},
		{"unrelated", other, false},
		{"glob exact", filepath.Join(root, "gone", "cache"), true},
		{"glob below", filepath.Join(root, "gone", "cache", "deep"), true},
		{"glob contains", filepath.Join(root, "gone"), true},
		{"glob miss", filepath.Join(root, "other", "cache"), false},
		{"symlink to excluded", link, true},
		{"below symlink", filepath.Join(link, "z"), true},
		{"non-existent stored", ghost, true},
		{"below non-existent", filepath.Join(ghost, "y"), true},
	}
	for _, c := range cases {
		if _, got := l.Match(c.path); got != c.want {
			t.Errorf("%s: Match(%s)=%v want %v", c.name, c.path, got, c.want)
		}
	}
	if m, _ := l.Match(keep); m != keep {
		t.Errorf("matched entry %q, want %q", m, keep)
	}
}

func TestAddExpandsTildeAndRelative(t *testing.T) {
	home := testutil.TempHome(t)
	l, _ := Load(config.Paths{Config: filepath.Join(home, ".config", "sapulidi", "config.toml")})
	real := mkdir(t, filepath.Join(home, "work"))
	if _, _, err := l.Add("~/work", "", day); err != nil {
		t.Fatal(err)
	}
	if got := l.Entries()[0].Path; got != real {
		t.Fatalf("tilde: %q want %q", got, real)
	}
	t.Chdir(real)
	mkdir(t, filepath.Join(real, "rel"))
	if _, _, err := l.Add("rel", "", day); err != nil {
		t.Fatal(err)
	}
	// "rel" is below ~/work, already excluded: still one entry.
	if len(l.Entries()) != 1 {
		t.Fatalf("%+v", l.Entries())
	}
	if _, ok := l.Match(filepath.Join(real, "rel")); !ok {
		t.Fatal("relative path not matched")
	}
}

func TestLoadCorrupt(t *testing.T) {
	check := func(t *testing.T, p config.Paths) {
		t.Helper()
		_, err := Load(p)
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("want *CorruptError, got %v", err)
		}
		if ce.File != filepath.Join(filepath.Dir(p.Config), "exclude.toml") {
			t.Fatalf("file %q", ce.File)
		}
	}
	t.Run("bad toml", func(t *testing.T) {
		p := tempPaths(t)
		os.MkdirAll(filepath.Dir(p.Config), 0o700)
		os.WriteFile(filepath.Join(filepath.Dir(p.Config), "exclude.toml"), []byte("[[exclude\nx"), 0o600)
		check(t, p)
	})
	t.Run("directory", func(t *testing.T) {
		p := tempPaths(t)
		os.MkdirAll(filepath.Join(filepath.Dir(p.Config), "exclude.toml"), 0o700)
		check(t, p)
	})
	t.Run("unreadable", func(t *testing.T) {
		p := tempPaths(t)
		f := filepath.Join(filepath.Dir(p.Config), "exclude.toml")
		os.MkdirAll(filepath.Dir(f), 0o700)
		os.WriteFile(f, []byte(""), 0o000)
		if _, err := os.ReadFile(f); err == nil {
			t.Skip("running as a user that can read mode 000 files")
		}
		check(t, p)
	})
	t.Run("relative path entry", func(t *testing.T) {
		p := tempPaths(t)
		os.MkdirAll(filepath.Dir(p.Config), 0o700)
		os.WriteFile(filepath.Join(filepath.Dir(p.Config), "exclude.toml"), []byte("[[exclude]]\npath = \"rel\"\nadded = \"2026-01-01\"\n"), 0o600)
		check(t, p)
	})
}
