package guard

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
)

// GitTimeout bounds every git call a guard makes.
var GitTimeout = 15 * time.Second

// GitArgv is the one way sapulidi starts git on a repository it does not own: the argv for `git -C dir args...`
// with the repository's core.fsmonitor, hooks and pager switched off (a repository unpacked from an archive can name
// a command for each in its own .git/config). The user's global config stays: its ignore rules matter.
// The system config is switched off by execx.Real, which sets GIT_CONFIG_NOSYSTEM=1 for a git child; building an
// argv changes nothing in the process, and argv[0] stays "git" for every Runner.
//
// Limits (docs/known-limits.md): this is not a sandbox. A filter driver (filter.<name>.clean or .process in
// .git/config plus a .gitattributes in the repository) can still run during `git status` on racy-clean files, and
// core.attributesFile, diff.external and textconv are not covered. Running git in any untrusted repository has the
// same exposure.
func GitArgv(dir string, args ...string) []string {
	return append([]string{"git", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "core.pager=cat",
		"-C", dir}, args...)
}

func gitOut(run core.Runner, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	out, err := run.Run(ctx, dir, GitArgv(dir, args...)...)
	return string(out), err
}

func exitCode(err error) int {
	var ee *execx.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

func gitFail(err error) error { return core.Keep("gitfail", err.Error()) }

func notARepo(err error) bool {
	var ee *execx.ExitError
	return errors.As(err, &ee) && ee.Code == 128 && strings.Contains(ee.Stderr, "not a git repository")
}
