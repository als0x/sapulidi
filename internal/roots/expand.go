package roots

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// expand returns the repositories inside the history folder c, which is not one itself, as candidates
// with source "<agent>+children". At most maxPerExpanded are taken, in path order.
func expand(env DiscoverEnv, c cand, over func() bool, drop func(path, why string)) []cand {
	var out []cand
	for _, dir := range repoDirs(c.path, over) {
		if over() {
			break
		}
		if len(out) == maxPerExpanded {
			drop(dir, "cap")
			continue
		}
		top, why := repoRoot(env.Run, dir)
		if why != "" {
			drop(dir, why)
			continue
		}
		out = append(out, cand{path: top, source: c.source + childrenSuffix})
	}
	return out
}

// repoDirs lists, sorted, the directories holding a `.git` entry: children of dir, and children of a
// child without one. Hidden directories and symlinks are skipped; a repository is never descended into.
func repoDirs(dir string, over func() bool) []string {
	var repos []string
	var walk func(string, int)
	walk = func(parent string, depth int) {
		entries, _ := os.ReadDir(parent) // an unreadable directory has no repositories to offer
		for _, e := range entries {
			if over() {
				return
			}
			// IsDir is false for a symlink, whatever it points to
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(parent, e.Name())
			if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
				repos = append(repos, p)
			} else if depth == 1 {
				walk(p, 2)
			}
		}
	}
	walk(dir, 1)
	slices.Sort(repos)
	return repos
}
