package guard

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
)

func hasMarker(parent string, markers []string) (bool, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		for _, m := range markers {
			if ok, _ := filepath.Match(m, e.Name()); ok {
				return true, nil
			}
		}
	}
	return false, nil
}

func resolveGitDir(parent, p string) string {
	p = strings.TrimSpace(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(parent, p)
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// ProjectArtifact is spec S5 for per-project artefacts: marker next to it, inside a git work tree
// that is not a linked worktree, git-ignored, with no tracked file, and every nested repository inside clean and
// fully pushed (nestedProof).
func ProjectArtifact(run core.Runner, dir string, markers []string) error {
	if di, err := os.Lstat(dir); err != nil {
		return core.Keep("error", err.Error())
	} else if !di.IsDir() {
		return core.Keep("not-ignored", dir+" is a symlink or not a directory")
	}
	parent := filepath.Dir(dir)
	base := filepath.Base(dir)
	ok, err := hasMarker(parent, markers)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	if !ok {
		return core.Keep("no-marker", parent)
	}
	out, err := gitOut(run, parent, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if notARepo(err) {
			return core.Keep("not-ignored", "parent is not a git repository")
		}
		return gitFail(err)
	}
	if strings.TrimSpace(out) != "true" {
		return core.Keep("not-ignored", "parent is not inside a work tree")
	}
	out, err = gitOut(run, parent, "rev-parse", "--git-dir", "--git-common-dir")
	if err != nil {
		return gitFail(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return core.Keep("gitfail", "unexpected rev-parse output")
	}
	if resolveGitDir(parent, lines[0]) != resolveGitDir(parent, lines[1]) {
		return core.Keep("not-ignored", "parent repository is a linked worktree")
	}
	// ls-files runs before check-ignore: git reports a directory holding tracked files as not ignored,
	// which would hide the more precise "tracked".
	out, err = gitOut(run, dir, "ls-files")
	if err != nil {
		return gitFail(err)
	}
	if strings.TrimSpace(out) != "" {
		return core.Keep("tracked", "tracked files inside "+base)
	}
	if _, err = gitOut(run, parent, "check-ignore", "-q", "--", base); err != nil {
		if exitCode(err) == 1 {
			return core.Keep("not-ignored", base+" is not git-ignored")
		}
		return gitFail(err)
	}
	return nestedProof(run, dir)
}

// HoldsRepo reports whether path, or anything inside it, is a git repository: a .git entry or a bare
// repository (HEAD and objects). A walk error, a cancelled ctx included, is returned and callers keep. A nil ctx
// never cancels.
func HoldsRepo(ctx context.Context, path string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	found := false
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Name() == ".git" || (d.IsDir() && isBareRepo(p)) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found, err
}

// isBareRepo reports a directory that holds both HEAD and objects (a bare repository or mirror).
func isBareRepo(dir string) bool {
	_, e1 := os.Lstat(filepath.Join(dir, "HEAD"))
	oi, e2 := os.Lstat(filepath.Join(dir, "objects"))
	return e1 == nil && e2 == nil && oi.IsDir()
}
