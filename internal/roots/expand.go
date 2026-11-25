package roots

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// personalDirs are the folders of home that are never expanded, nor is anything below them.
var personalDirs = []string{"Downloads", "Desktop", "Documents", "Library", "Pictures", "Movies", "Music", "Public"}

// personal reports whether p is, or is below, one of personalDirs, as written or after resolving symlinks.
// fold is the case folding of the filesystem.
func personal(home, p string, fold func(string) string) bool {
	for _, name := range personalDirs {
		d := filepath.Join(home, name)
		if underAny(fold(filepath.Clean(p)), []string{fold(d)}) || underAny(fold(realPath(p)), []string{fold(realPath(d))}) {
			return true
		}
	}
	return false
}

// holdsGit reports whether dir, or an ancestor up to home, has a `.git` entry of any kind. Git rejecting
// such a directory means a repository it cannot read, not a plain folder.
func holdsGit(home, dir string, fold func(string) string) bool {
	stop := fold(realPath(home))
	for p := realPath(dir); ; p = filepath.Dir(p) {
		if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
			return true
		}
		if fold(p) == stop || p == filepath.Dir(p) {
			return false
		}
	}
}

// expand returns the repositories inside the history folder c, which is not one itself, as candidates
// with source "<agent>+children". At most maxPerExpanded are taken, in path order; a repository whose
// root is not inside the folder is dropped.
func expand(env DiscoverEnv, c cand, keyOf func(string) string, over func() bool, drop func(path, why string)) []cand {
	dirs, more := repoDirs(c.path, over)
	folder := keyOf(c.path)
	var out []cand
	for _, dir := range dirs {
		if over() {
			return out
		}
		if len(out) == maxPerExpanded {
			more = true
			break
		}
		top, why := repoRoot(env.Run, dir)
		if why != "" {
			drop(dir, why)
			continue
		}
		// a linked worktree maps to its main repository, which may be anywhere
		if !under(keyOf(top), folder) {
			drop(top, "outside")
			continue
		}
		out = append(out, cand{path: top, source: c.source + childrenSuffix})
	}
	if more {
		drop(c.path, "cap")
	}
	return out
}

// repoDirs lists, sorted, the directories whose `.git` is a file or a directory: children of dir, and
// children of a child without a `.git`. Hidden directories and symlinks are skipped; a repository is never
// descended into. It collects at most maxCollected and reports whether there were more.
func repoDirs(dir string, over func() bool) (repos []string, more bool) {
	var walk func(string, int)
	walk = func(parent string, depth int) {
		entries, _ := os.ReadDir(parent) // an unreadable directory has no repositories to offer
		for _, e := range entries {
			if more || over() {
				return
			}
			// IsDir is false for a symlink, whatever it points to
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(parent, e.Name())
			git, err := os.Lstat(filepath.Join(p, ".git"))
			switch {
			case errors.Is(err, fs.ErrNotExist):
				if depth == 1 {
					walk(p, 2)
				}
			case err != nil || !git.IsDir() && !git.Mode().IsRegular():
				// something else, such as a symlink: neither taken nor searched
			case len(repos) == maxCollected:
				more = true
			default:
				repos = append(repos, p)
			}
		}
	}
	walk(dir, 1)
	slices.Sort(repos)
	return repos, more
}
