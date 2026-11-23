package guard

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

// WorktreeBlocker is spec S9: it returns the reason a linked worktree must stay, or nil.
func WorktreeBlocker(run core.Runner, dir string, now time.Time, age time.Duration) error {
	dotgit := filepath.Join(dir, ".git")
	fi, err := os.Lstat(dotgit)
	if err != nil || !fi.Mode().IsRegular() {
		return core.Keep("orphan", ".git is not a file in "+dir)
	}
	b, err := os.ReadFile(dotgit)
	if err != nil {
		return core.Keep("orphan", err.Error())
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return core.Keep("orphan", ".git file has no gitdir")
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	if _, err := os.Stat(target); err != nil {
		return core.Keep("orphan", "gitdir is gone: "+target)
	}

	out, err := gitOut(run, dir, "log", "-1", "--format=%ct")
	if err != nil {
		return gitFail(err)
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return core.Keep("gitfail", "bad commit time: "+strings.TrimSpace(out))
	}
	if now.Sub(time.Unix(secs, 0)) < age {
		return core.Keep("young", "last commit is recent")
	}
	if err := Fresh(dir, age, now); err != nil {
		return err
	}

	out, err = gitOut(run, dir, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return gitFail(err)
	}
	if lines := nonEmptyLines(out); len(lines) > 0 {
		for _, l := range lines {
			if !strings.HasPrefix(l, "??") {
				return core.Keep("dirty", "uncommitted changes")
			}
		}
		return core.Keep("untracked", "untracked files")
	}

	out, err = gitOut(run, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return gitFail(err)
	}
	branch := strings.TrimSpace(out)
	if branch == "HEAD" {
		branch = "(no branch)"
	}
	out, err = gitOut(run, dir, "stash", "list", "--format=%gs")
	if err != nil {
		return gitFail(err)
	}
	for _, l := range nonEmptyLines(out) {
		if strings.HasPrefix(l, "WIP on "+branch+":") || strings.HasPrefix(l, "On "+branch+":") {
			return core.Keep("stash", "stash entry for "+branch)
		}
	}

	out, err = gitOut(run, dir, "rev-list", "HEAD", "--not", "--remotes")
	if err != nil {
		return gitFail(err)
	}
	if strings.TrimSpace(out) != "" {
		return core.Keep("unpushed", "commits not on any remote")
	}
	return nil
}

func nonEmptyLines(s string) []string {
	var r []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			r = append(r, l)
		}
	}
	return r
}
