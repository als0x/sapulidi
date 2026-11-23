package delivery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/als0x/sapulidi/internal/config"
)

// ownDirName is the base name every directory sapulidi removes from must have. A SAPULIDI_CONFIG, SAPULIDI_STATE
// or SAPULIDI_DATA override that points anywhere else (the home directory, ~/Documents, /usr/local, /etc, ...) is
// refused, because these functions delete files and must never be steered at a directory the user owns.
const ownDirName = "sapulidi"

// versionName is the only form of a directory name under <data>/bin that sapulidi installs and may remove.
var versionName = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// CheckPaths refuses, with a message that names the directory, unless the config, state and data directories are
// each safe to remove from (see checkDir) and none lies inside or equals another. home "" means the user's home.
func CheckPaths(p config.Paths, home string) error {
	home, err := homeOf(home)
	if err != nil {
		return err
	}
	dirs := []struct{ label, dir string }{
		{"config", filepath.Dir(p.Config)},
		{"state", p.State},
		{"data", p.Data},
	}
	if p.Config == "" {
		dirs[0].dir = ""
	}
	for _, d := range dirs {
		if err := checkDir(d.label, d.dir, home); err != nil {
			return err
		}
	}
	for i := range dirs {
		for j := i + 1; j < len(dirs); j++ {
			a, b := realPath(dirs[i].dir), realPath(dirs[j].dir)
			if within(a, b) || within(b, a) {
				return fmt.Errorf("refusing to remove anything: the %s directory %q and the %s directory %q overlap",
					dirs[i].label, dirs[i].dir, dirs[j].label, dirs[j].dir)
			}
		}
	}
	return nil
}

// checkDir refuses a directory that is not absolute, is "/", is the home directory or an ancestor of it, or whose
// base name (after resolving symlinks) is not exactly "sapulidi".
func checkDir(label, dir, home string) error {
	refuse := func(why string) error {
		return fmt.Errorf("refusing to remove anything: the %s directory %q %s (sapulidi only works inside a directory named %q)",
			label, dir, why, ownDirName)
	}
	if !filepath.IsAbs(dir) {
		return refuse("is not an absolute path")
	}
	for _, c := range []string{filepath.Clean(dir), realPath(dir)} {
		if filepath.Dir(c) == c {
			return refuse("is the file system root")
		}
		for _, h := range []string{filepath.Clean(home), realPath(home)} {
			if within(h, c) {
				return refuse("is your home directory or contains it")
			}
		}
		if filepath.Base(c) != ownDirName {
			return refuse("is not named " + ownDirName)
		}
	}
	return nil
}

// CheckOwned is what `uninstall --all` asks before it removes anything: none of the state, config or data path is
// itself a symlink (it would be followed somewhere else), and each existing directory holds something only sapulidi
// writes: a state file, the config files, or a binary version directory (or the launcher's lock) under bin/.
func CheckOwned(p config.Paths) error {
	for _, d := range []struct{ label, path string }{
		{"state", p.State}, {"config", filepath.Dir(p.Config)}, {"config file", p.Config}, {"data", p.Data},
	} {
		if p.Config == "" && d.label != "state" && d.label != "data" {
			continue
		}
		if fi, err := os.Lstat(d.path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to remove anything: the %s path %q is a symlink", d.label, d.path)
		}
	}
	if err := CheckStateMarker(p.State); err != nil {
		return err
	}
	if p.Config != "" {
		cdir := filepath.Dir(p.Config)
		if _, err := os.Lstat(cdir); err == nil && !isRegular(p.Config) && !isRegular(config.AppliedPath(p.Config)) {
			return fmt.Errorf("refusing to remove anything: the config directory %q holds neither %s nor %s, so it does not "+
				"look like sapulidi's config; remove it by hand if it is", cdir, filepath.Base(p.Config),
				filepath.Base(config.AppliedPath(p.Config)))
		}
	}
	if _, err := os.Lstat(p.Data); err == nil && !hasBinMarker(filepath.Join(p.Data, "bin")) {
		return fmt.Errorf("refusing to remove anything: the data directory %q has no bin/ with a version directory or the "+
			"launcher lock, so it does not look like sapulidi's data; remove it by hand if it is", p.Data)
	}
	return nil
}

func isRegular(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}

// hasBinMarker reports a bin directory with a version-named subdirectory or the launcher's .lock directory.
func hasBinMarker(bin string) bool {
	entries, err := os.ReadDir(bin)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && (versionName.MatchString(e.Name()) || e.Name() == ".lock") {
			return true
		}
	}
	return false
}

// stateMarkers are files only sapulidi writes into its state directory.
var stateMarkers = []string{"consent.json", "log.jsonl", "lastrun.json"}

// CheckStateMarker refuses an existing state directory that holds none of the files only sapulidi writes: a folder
// named "sapulidi" that is somebody's checkout is not our state, and the whole directory is about to be removed.
// A state directory that does not exist passes (nothing to remove).
func CheckStateMarker(state string) error {
	if _, err := os.Lstat(state); err != nil {
		return nil
	}
	for _, m := range stateMarkers {
		if fi, err := os.Lstat(filepath.Join(state, m)); err == nil && fi.Mode().IsRegular() {
			return nil
		}
	}
	return fmt.Errorf("refusing to remove anything: the state directory %q holds none of %s, so it does not look like "+
		"sapulidi's state; remove it by hand if it is", state, strings.Join(stateMarkers, ", "))
}

func homeOf(home string) (string, error) {
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil || h == "" {
			return "", fmt.Errorf("refusing to remove anything: the home directory is unknown")
		}
		home = h
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("refusing to remove anything: the home directory %q is not absolute", home)
	}
	return home, nil
}

// realPath resolves symlinks in the longest existing prefix of p and appends the rest.
func realPath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// within reports whether path is dir or lies under it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
