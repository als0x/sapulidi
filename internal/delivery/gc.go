// Package delivery owns sapulidi's own binaries: removing the old ones and uninstalling (spec 4.1). It removes
// only files sapulidi installed itself, never user data.
package delivery

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/state/atomic"
)

const (
	// gcMaxAge is how long a version directory must be untouched before it can be removed.
	gcMaxAge = 30 * 24 * time.Hour
	// gcInterval is the least time between two GC runs.
	gcInterval = 24 * time.Hour
	gcStamp    = "gc-last"
)

// GCOptions says what GC may remove.
type GCOptions struct {
	Data    string    // <data>; versions live in <data>/bin/<version>/
	Home    string    // the user's home directory; "" means os.UserHomeDir()
	Now     time.Time // zero means time.Now()
	Named   []string  // versions a host heartbeat names
	Running string    // path of the running executable; its version directory is kept ("" when unknown)
}

// GC removes <data>/bin/<version>/ directories that no heartbeat names, that are not `current`, that do not hold
// the running binary and whose newest file is older than 30 days. Only directories named like a release
// (1.2.3, 1.2.3-rc.1) are considered; every other entry in bin stays. A data directory that fails the path guard
// is refused with an error and nothing is removed. A directory that cannot be read completely counts as fresh.
// It returns the removed version names.
func GC(o GCOptions) ([]string, error) {
	home, err := homeOf(o.Home)
	if err != nil {
		return nil, err
	}
	if err := checkDir("data", o.Data, home); err != nil {
		return nil, err
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	bin := filepath.Join(o.Data, "bin")
	entries, err := os.ReadDir(bin)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	current, _ := filepath.EvalSymlinks(filepath.Join(bin, "current"))
	running := versionOf(bin, o.Running)
	var removed []string
	for _, e := range entries {
		name := e.Name()
		// e.IsDir is false for a symlink, so a link is never followed.
		if !versionName.MatchString(name) || !e.IsDir() || name == running || named(o.Named, name) {
			continue
		}
		dir := filepath.Join(bin, name)
		if current != "" {
			if r, err := filepath.EvalSymlinks(dir); err == nil && r == current {
				continue
			}
		}
		if t, ok := newest(dir); !ok || t.After(now.Add(-gcMaxAge)) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return removed, err
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// GCDaily is GC at most once a day: <state>/gc-last records the last run. ran says whether GC ran. Both the
// state and the data directory must pass the path guard before the stamp is written.
func GCDaily(stateDir string, o GCOptions) (removed []string, ran bool, err error) {
	home, err := homeOf(o.Home)
	if err != nil {
		return nil, false, err
	}
	if err := checkDir("state", stateDir, home); err != nil {
		return nil, false, err
	}
	if err := checkDir("data", o.Data, home); err != nil {
		return nil, false, err
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	stamp := filepath.Join(stateDir, gcStamp)
	if fi, err := os.Stat(stamp); err == nil && now.Sub(fi.ModTime()) < gcInterval && !fi.ModTime().After(now) {
		return nil, false, nil
	}
	if err := atomic.WriteFile(stamp, []byte(now.UTC().Format(time.RFC3339))); err != nil {
		return nil, false, err
	}
	_ = os.Chtimes(stamp, now, now)
	removed, err = GC(o)
	return removed, true, err
}

// named reports whether a heartbeat version names a directory.
func named(versions []string, dir string) bool {
	for _, v := range versions {
		if v == dir || strings.TrimPrefix(v, "v") == dir {
			return true
		}
	}
	return false
}

// versionOf returns the <version> directory name of exe when it lives under bin, else "".
func versionOf(bin, exe string) string {
	if exe == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if r, err := filepath.EvalSymlinks(bin); err == nil {
		bin = r
	}
	rel, err := filepath.Rel(bin, exe)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	first, _, _ := strings.Cut(rel, string(filepath.Separator))
	return first
}

// newest is the latest mtime of dir and everything under it; symlinks are not followed. ok is false when any
// part could not be read or no time was found: the caller must then treat the directory as fresh.
func newest(dir string) (t time.Time, ok bool) {
	ok = true
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			ok = false
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			ok = false
			return nil
		}
		if fi.ModTime().After(t) {
			t = fi.ModTime()
		}
		return nil
	})
	return t, ok && err == nil && !t.IsZero()
}
