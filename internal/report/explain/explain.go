// Package explain gives facts about one path for `sapulidi explain`: size, owner, newest change, git status, the
// rule that would match it and the guard that would keep it. It only reads, never prints file contents, runs
// git with the repository's own configuration switched off, and cleans every text it returns (file names are
// untrusted).
package explain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/categories/models"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/guard"
	"github.com/als0x/sapulidi/internal/report"
	"github.com/als0x/sapulidi/internal/report/sizing"
)

// Git says whether the path is inside a repository and what git thinks of it: ignored, tracked or untracked.
type Git struct {
	Repo   string `json:"repo"`
	Status string `json:"status"`
}

// Keep is the guard that would keep the path, as a Kept token and its detail.
type Keep struct {
	Why    string `json:"why"`
	Detail string `json:"detail,omitempty"`
}

type Explain struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"` // dir | file
	Bytes     int64  `json:"bytes"`
	SizeKnown bool   `json:"size_known"`
	Truncated bool   `json:"truncated"` // the 30 s budget ended: size is partial and slow guards were skipped
	Owner     string `json:"owner,omitempty"`
	Newest    string `json:"newest_change,omitempty"` // RFC 3339
	Git       *Git   `json:"git,omitempty"`
	Rule      string `json:"rule,omitempty"` // Rule.Key() of the rule that would match
	Keeps     *Keep  `json:"keeps,omitempty"`
}

const budget = 30 * time.Second

// gitPrefix runs git without the repository's fsmonitor, hooks or pager and without the system config.
var gitPrefix = []string{"env", "GIT_CONFIG_NOSYSTEM=1", "git", "-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=/dev/null", "-c", "core.pager=cat"}

func refuse(format string, a ...any) error {
	return errors.New(report.Clean(fmt.Sprintf(format, a...)))
}

// Path explains one absolute path strictly below home, or at or below a root. Home itself, ~/Library and
// anything else is refused.
func Path(env *core.Env, path string) (Explain, error) {
	if !filepath.IsAbs(path) {
		return Explain{}, refuse("explain: the path must be absolute")
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return Explain{}, refuse("explain: %v", err)
	}
	if !allowed(env, real) {
		return Explain{}, refuse("explain: only paths below home or a root can be explained")
	}
	fi, err := os.Lstat(real)
	if err != nil {
		return Explain{}, refuse("explain: %v", err)
	}
	ctx, cancel := context.WithTimeout(sizing.Parent(env.Ctx), budget)
	defer cancel()

	e := Explain{Path: real, Kind: "file"}
	if fi.IsDir() {
		e.Kind = "dir"
	}
	e.Bytes, e.SizeKnown = sizing.Dir(ctx, real)
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		e.Owner = strconv.Itoa(int(st.Uid))
		if u, err := user.LookupId(e.Owner); err == nil {
			e.Owner = u.Username
		}
	}
	slow := ctx.Err() == nil // Newest and Secret walk the tree like the size did; skip them after a timeout
	if slow {
		var newest time.Time
		if sizing.Within(ctx, func() { newest, _ = guard.Newest(real) }) && !newest.IsZero() {
			e.Newest = newest.UTC().Format(time.RFC3339)
		}
	}
	ownedRepo, top := repoOf(env, real, fi.IsDir())
	if ownedRepo {
		e.Git = gitOf(ctx, env, real, fi.IsDir(), top)
	}
	rule := matchRule(env, real)
	if rule != nil {
		e.Rule = rule.Key()
	}
	if err := keeps(ctx, env, real, rule, slow && ctx.Err() == nil, top == "" || ownedRepo); err != nil {
		var ke *core.KeepError
		if errors.As(err, &ke) {
			e.Keeps = &Keep{Why: ke.Why, Detail: ke.Detail}
		} else {
			e.Keeps = &Keep{Why: "error", Detail: err.Error()}
		}
	}
	e.Truncated = ctx.Err() != nil || !slow
	if e.Truncated {
		e.SizeKnown = false
	}
	e.clean()
	return e, nil
}

func (e *Explain) clean() {
	e.Path, e.Owner, e.Rule = report.Clean(e.Path), report.Clean(e.Owner), report.Clean(e.Rule)
	if e.Git != nil {
		e.Git.Repo, e.Git.Status = report.Clean(e.Git.Repo), report.Clean(e.Git.Status)
	}
	if e.Keeps != nil {
		e.Keeps.Why, e.Keeps.Detail = report.Clean(e.Keeps.Why), report.Clean(e.Keeps.Detail)
	}
}

func under(dir, p string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func allowed(env *core.Env, real string) bool {
	for _, r := range env.Roots {
		if under(resolved(r.Path), real) {
			return true
		}
	}
	if env.Home == "" {
		return false
	}
	home := resolved(env.Home)
	if !under(home, real) || real == home {
		return false
	}
	return !strings.EqualFold(strings.TrimPrefix(real, home+string(filepath.Separator)), "Library")
}

// repoOf finds the repository top above p by looking for .git, and says whether the user owns it. A repository
// that is not owned (or whose owner cannot be checked) is never given to git.
func repoOf(env *core.Env, p string, isDir bool) (owned bool, top string) {
	dir := p
	if !isDir {
		dir = filepath.Dir(p)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			top = d
			break
		}
		if d == filepath.Dir(d) {
			return false, ""
		}
	}
	if env.Platform == nil {
		return false, top
	}
	ok, err := env.Platform.OwnedByUser(top)
	return ok && err == nil, top
}

func gitOf(ctx context.Context, env *core.Env, p string, isDir bool, top string) *Git {
	if env.Run == nil {
		return nil
	}
	dir := p
	if !isDir {
		dir = filepath.Dir(p)
	}
	run := func(args ...string) ([]byte, error) {
		return env.Run.Run(ctx, dir, append(append([]string(nil), gitPrefix...), args...)...)
	}
	g := &Git{Repo: top, Status: "untracked"}
	if _, err := run("check-ignore", "-q", "--", p); err == nil {
		g.Status = "ignored"
	} else if out, err := run("ls-files", "--", p); err == nil && strings.TrimSpace(string(out)) != "" {
		g.Status = "tracked"
	}
	return g
}

// matchRule finds the rule whose shape fits the path: a model store, a temp entry, or a project or file artefact
// inside a root next to its marker. Path, cache and versions rules are decided by their categories and are not
// matched here.
func matchRule(env *core.Env, p string) *core.Rule {
	base, parent := filepath.Base(p), filepath.Dir(p)
	for _, s := range models.Stores(env) {
		if under(resolved(s.Path), p) {
			for i := range env.Rules {
				if r := &env.Rules[i]; r.Category == "models" && r.Location == s.Location {
					return r
				}
			}
		}
	}
	for i := range env.Rules {
		r := &env.Rules[i]
		if !globAny(r.Names, base) {
			continue
		}
		switch r.Kind {
		case core.KindTemp:
			if env.TempDir != "" && parent == resolved(env.TempDir) {
				return r
			}
		case core.KindProject, core.KindFile:
			if inRoot(env, p) && markerIn(parent, r.Markers) {
				return r
			}
		}
	}
	return nil
}

func globAny(patterns []string, name string) bool {
	for _, pat := range patterns {
		if ok, _ := filepath.Match(pat, name); ok {
			return true
		}
	}
	return false
}

func inRoot(env *core.Env, p string) bool {
	for _, r := range env.Roots {
		if under(resolved(r.Path), p) && p != resolved(r.Path) {
			return true
		}
	}
	return false
}

func markerIn(dir string, markers []string) bool {
	for _, m := range markers {
		if hits, _ := filepath.Glob(filepath.Join(dir, m)); len(hits) > 0 {
			return true
		}
	}
	return false
}

// check runs one guard under ctx; a guard that does not finish in time counts as "budget".
func check(ctx context.Context, fn func() error) error {
	var err error
	if !sizing.Within(ctx, func() { err = fn() }) {
		return core.Keep("budget", "the 30 s limit ended before the check finished")
	}
	return err
}

// keeps runs the guards in the order of a removal and returns the first one that would keep the path. The guards
// that walk the tree (secret, young, project) are skipped when slow is false; gitOK is false for a repository the
// user does not own.
func keeps(ctx context.Context, env *core.Env, p string, rule *core.Rule, slow, gitOK bool) error {
	if err := guard.Deny(env.Home, env.TempDir, p); err != nil {
		return err
	}
	if rule != nil && (rule.Bucket == core.Never || rule.Category == "models") {
		return core.Keep("deny", "never bucket: "+rule.Key())
	}
	if slow {
		if err := check(ctx, func() error { return guard.Secret(p) }); err != nil {
			return err
		}
		if rule != nil {
			min := time.Hour
			if rule.Bucket == core.Optional {
				min = 24 * time.Hour
			}
			if rule.MinAge > min {
				min = rule.MinAge
			}
			if err := check(ctx, func() error { return guard.Fresh(p, min, env.Now) }); err != nil {
				return err
			}
		}
	}
	if env.Platform != nil {
		if err := check(ctx, func() error { return guard.InUse(env.Platform, p, true) }); err != nil {
			return err
		}
	}
	if rule != nil && rule.Kind == core.KindProject && env.Run != nil && slow {
		if !gitOK {
			return core.Keep("error", "the repository is not owned by the user")
		}
		return check(ctx, func() error { return guard.ProjectArtifact(env.Run, p, rule.Markers) })
	}
	return nil
}
