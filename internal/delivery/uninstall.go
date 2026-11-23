package delivery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/schedule"
)

// UninstallOptions says what Uninstall removes.
type UninstallOptions struct {
	Paths   config.Paths
	Home    string // the user's home directory; "" means os.UserHomeDir()
	Backend schedule.Backend
	All     bool // also config and state
}

// UninstallResult lists what was removed, in order.
type UninstallResult struct {
	Schedule []string // labels removed from the scheduler
	Removed  []string // paths removed
}

// currentTemp is the temporary link name the launcher makes before renaming it over "current".
var currentTemp = regexp.MustCompile(`^\.current\.[0-9]+$`)

// Uninstall removes the scheduled jobs, then the binaries, and with All the config files and the state directory.
// Before anything is removed CheckPaths must accept all three directories. Without All it removes only version
// directories named like a release, the current link and the launcher's own lock and download leftovers inside
// <data>/bin; any other entry stays. The caller has already confirmed All in a terminal. User pack files next to
// config.toml are never removed.
func Uninstall(o UninstallOptions) (UninstallResult, error) {
	var res UninstallResult
	p := o.Paths
	if err := CheckPaths(p, o.Home); err != nil {
		return res, err
	}
	if o.All {
		if err := CheckOwned(p); err != nil {
			return res, err
		}
	}
	for _, j := range schedule.Jobs(p) {
		if err := o.Backend.Remove(j.Label); err != nil {
			return res, fmt.Errorf("schedule remove %s: %w", j.Label, err)
		}
		res.Schedule = append(res.Schedule, j.Label)
	}
	if err := removeBinaries(filepath.Join(p.Data, "bin"), &res); err != nil {
		return res, err
	}
	_ = os.Remove(p.Data) // only when empty
	if !o.All {
		return res, nil
	}
	for _, f := range []string{p.Config, config.AppliedPath(p.Config)} {
		if _, err := os.Lstat(f); err != nil {
			continue
		}
		if err := os.Remove(f); err != nil {
			return res, err
		}
		res.Removed = append(res.Removed, f)
	}
	_ = os.Remove(filepath.Dir(p.Config)) // only when empty: user packs keep it
	if _, err := os.Lstat(p.State); err == nil {
		// CheckPaths made sure the directory is named sapulidi, is not the home and holds no other of our directories.
		if err := os.RemoveAll(p.State); err != nil {
			return res, err
		}
		res.Removed = append(res.Removed, p.State)
	}
	return res, nil
}

// removeBinaries removes only what the launcher and GC create inside bin, never bin as a whole.
func removeBinaries(bin string, res *UninstallResult) error {
	entries, err := os.ReadDir(bin)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(bin, name)
		fi, err := os.Lstat(path)
		if err != nil {
			continue
		}
		mode := fi.Mode()
		var remove func(string) error
		switch {
		case versionName.MatchString(name) && mode.IsDir():
			remove = os.RemoveAll
		case (name == "current" || currentTemp.MatchString(name)) && mode&os.ModeSymlink != 0:
			remove = os.Remove
		case name == ".lock" && mode.IsDir():
			remove = os.Remove // the launcher's lock directory; Remove fails, harmlessly, when it is not empty
		case (name == ".download" || strings.HasPrefix(name, ".download.")) && mode.IsRegular():
			remove = os.Remove
		default:
			continue
		}
		if err := remove(path); err != nil {
			if name == ".lock" {
				continue
			}
			return err
		}
		res.Removed = append(res.Removed, path)
	}
	_ = os.Remove(bin) // only when nothing else is in it
	return nil
}
