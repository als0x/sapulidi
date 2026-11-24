package guard

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

// NestedBudget bounds the whole proof about the repositories inside one project artefact.
var NestedBudget = 20 * time.Second

// MaxNested is the most nested repositories a project artefact may hold and still be proven.
const MaxNested = 16

type nestedRepo struct {
	path string // absolute
	kind string // "git" (a .git directory), "gitfile" or "bare"
}

// nestedRepos finds the repositories inside dir (dir itself included): a .git directory or file, or a bare
// repository. It follows no symlink, stays on dir's filesystem and does not look inside .git directories. A walk that
// finds more than MaxNested stops and reports tooMany.
func nestedRepos(ctx context.Context, dir string) (repos []nestedRepo, tooMany bool, err error) {
	var rootDev uint64
	var haveDev bool
	if fi, e := os.Lstat(dir); e == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			rootDev, haveDev = uint64(st.Dev), true //nolint:unconvert // Dev is int32 on darwin
		}
	}
	add := func(r nestedRepo) bool {
		if len(repos) == MaxNested {
			tooMany = true
			return false
		}
		repos = append(repos, r)
		return true
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Name() == ".git" {
			r := nestedRepo{path: filepath.Dir(p), kind: "git"}
			if !d.IsDir() {
				r.kind = "gitfile"
			}
			if !add(r) {
				return fs.SkipAll
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if haveDev && p != dir {
			if info, e := d.Info(); e == nil {
				if st, ok := info.Sys().(*syscall.Stat_t); ok && uint64(st.Dev) != rootDev { //nolint:unconvert
					return fs.SkipDir
				}
			}
		}
		if isBareRepo(p) {
			if !add(nestedRepo{path: p, kind: "bare"}) {
				return fs.SkipAll
			}
			return fs.SkipDir
		}
		return nil
	})
	return repos, tooMany, err
}

// nestedProof is the proof that every repository inside the project artefact dir is safe to discard: a clean work
// tree, no stash, a remote, and no commit (on a branch or on HEAD) that no remote branch holds. Anything else, a git
// error or the time budget included, keeps the artefact as nested-git, naming the first failing repository.
func nestedProof(run core.Runner, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), NestedBudget)
	defer cancel()
	repos, tooMany, err := nestedRepos(ctx, dir)
	if err != nil {
		return core.Keep("nested-git", "cannot search "+filepath.Base(dir)+" for repositories: "+err.Error())
	}
	if tooMany {
		return core.Keep("nested-git", "more than 16 nested repositories")
	}
	for _, r := range repos {
		if reason := nestedUnsafe(ctx, run, r); reason != "" {
			rel, e := filepath.Rel(dir, r.path)
			if e != nil {
				rel = r.path
			}
			return core.Keep("nested-git", rel+": "+reason)
		}
	}
	return nil
}

// nestedUnsafe returns why r cannot be discarded, or "" when it can.
func nestedUnsafe(ctx context.Context, run core.Runner, r nestedRepo) string {
	switch r.kind {
	case "gitfile":
		return "gitfile"
	case "bare":
		return "bare"
	}
	g := func(args ...string) (string, bool) {
		if ctx.Err() != nil {
			return "", false
		}
		out, err := gitOut(run, r.path, args...)
		return out, err == nil
	}
	out, ok := g("--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if !ok {
		return "error"
	}
	if strings.TrimSpace(out) != "" {
		for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
			if !strings.HasPrefix(line, "??") {
				return "dirty"
			}
		}
		return "untracked"
	}
	if out, ok = g("stash", "list"); !ok {
		return "error"
	} else if strings.TrimSpace(out) != "" {
		return "stash"
	}
	if out, ok = g("remote"); !ok {
		return "error"
	} else if strings.TrimSpace(out) == "" {
		return "no-remote"
	}
	if out, ok = g("rev-list", "--branches", "HEAD", "--not", "--remotes", "--max-count=1"); !ok {
		return "error"
	} else if strings.TrimSpace(out) != "" {
		return "unpushed"
	}
	return ""
}
