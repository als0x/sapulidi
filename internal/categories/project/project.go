// Package project is the generic implementation of the rules of kind `project` and `file`: directories (or
// files) inside the configured roots that sit next to a marker file and can be rebuilt (spec 3.1, S5, 5.3).
// One instance is registered per category name that uses it; the rules come from the packs. It proposes; the
// engine guards and removes.
package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/guard"
)

// Categories lists the category names this package registers.
var Categories = []string{"test-artefacts", "deps", "build-output", "crash", "code-index"}

var buckets = map[string]core.Bucket{
	"test-artefacts": core.Auto,
	"deps":           core.Optional,
	"build-output":   core.Optional,
	"crash":          core.Optional,
	"code-index":     core.Optional,
}

// gitTimeout bounds the git calls of file rules.
var gitTimeout = 15 * time.Second

type category struct {
	name   string
	bucket core.Bucket
}

func init() {
	for _, n := range Categories {
		core.Register(&category{name: n, bucket: buckets[n]})
	}
}

func (c *category) Name() string        { return c.name }
func (c *category) Bucket() core.Bucket { return c.bucket }

func matches(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

func isRuleKind(k core.RuleKind) bool { return k == core.KindProject || k == core.KindFile }

// rules returns the project and file rules of this category.
func (c *category) rules(env *core.Env) []core.Rule {
	var out []core.Rule
	for _, r := range env.Rules {
		if r.Category == c.name && isRuleKind(r.Kind) && r.Bucket != core.Never {
			out = append(out, r)
		}
	}
	return out
}

// pruneNames are the names of every `deps` and `build-output` project rule, on or off: their contents are
// never walked, so no walk offers a path inside another category's candidate.
func pruneNames(env *core.Env) []string {
	var out []string
	for _, r := range env.Rules {
		if (r.Category == "deps" || r.Category == "build-output") && r.Kind == core.KindProject {
			out = append(out, r.Names...)
		}
	}
	return out
}

// artefactNames are the names of every project rule of any category; a directory with such a name is an
// artefact, not project activity.
func artefactNames(env *core.Env) []string {
	var out []string
	for _, r := range env.Rules {
		if r.Kind == core.KindProject {
			out = append(out, r.Names...)
		}
	}
	return out
}

func ctxDone(env *core.Env) bool { return env.Ctx != nil && env.Ctx.Err() != nil }

// Scan walks each root once and proposes the entries a rule of this category names.
func (c *category) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	rules := c.rules(env)
	if len(rules) == 0 {
		return nil, nil, nil
	}
	prune := pruneNames(env)
	var cands []core.Candidate
	var kept []core.Kept
	for _, root := range env.Roots {
		if ctxDone(env) {
			break
		}
		rp := root.Path
		walkErr := filepath.WalkDir(rp, func(p string, d fs.DirEntry, err error) error {
			if ctxDone(env) {
				return fs.SkipAll
			}
			if err != nil {
				if d != nil && d.IsDir() && p != rp {
					return fs.SkipDir
				}
				return nil
			}
			name := d.Name()
			if p != rp && d.IsDir() {
				if name == ".git" || name == ".worktrees" {
					return fs.SkipDir
				}
				if same, err := env.Platform.SameDevice(rp, p); err != nil || !same {
					return fs.SkipDir
				}
			}
			if p == rp {
				return nil
			}
			isDir, isFile := d.IsDir(), d.Type().IsRegular()
			// Several rules can claim one name (target, build): try each in pack order, the first that
			// passes wins; otherwise keep once, preferring a failure after the marker was found.
			var rule *core.Rule
			var newest time.Time
			var failRule, offRule *core.Rule
			var offNewest time.Time
			var failErr error
			matched := false
			for i := range rules {
				r := &rules[i]
				if !((r.Kind == core.KindProject && isDir || r.Kind == core.KindFile && isFile) && matches(r.Names, name)) {
					continue
				}
				matched = true
				n, err := c.evaluate(env, *r, p)
				if err == nil {
					if env.Settings.RuleEnabled(r.Key()) {
						rule, newest = r, n
						break
					}
					// A switched-off rule is only a fallback: the engine drops its candidate.
					if offRule == nil {
						offRule, offNewest = r, n
					}
					continue
				}
				if failRule == nil || (isNoMarker(failErr) && !isNoMarker(err)) {
					failRule, failErr = r, err
				}
			}
			if !matched {
				if isDir && matches(prune, name) {
					return fs.SkipDir
				}
				return nil
			}
			if rule == nil && offRule != nil {
				rule, newest = offRule, offNewest
			}
			if rule == nil {
				kept = append(kept, keptOf(*failRule, p, failErr))
			} else {
				cands = append(cands, core.Candidate{
					Category: c.name, RuleKey: rule.Key(), Path: p, Base: rp, Removal: core.RenameDelete,
					Reason: fmt.Sprintf("%s of a project untouched for %s", name, since(env.Now, newest)),
				})
			}
			return skipIf(isDir)
		})
		if walkErr != nil && !isCtxErr(walkErr) {
			return cands, kept, walkErr
		}
	}
	return cands, kept, nil
}

func isNoMarker(err error) bool {
	var ke *core.KeepError
	return errors.As(err, &ke) && ke.Why == "no-marker"
}

func isCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func skipIf(isDir bool) error {
	if isDir {
		return fs.SkipDir
	}
	return nil
}

func keptOf(r core.Rule, p string, err error) core.Kept {
	why := "error"
	var ke *core.KeepError
	if errors.As(err, &ke) {
		why = ke.Why
	}
	return core.Kept{Category: r.Category, RuleKey: r.Key(), Path: p, Why: why}
}

func since(now, t time.Time) string {
	d := now.Sub(t)
	if d >= 24*time.Hour {
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
	return fmt.Sprintf("%d hours", int(d/time.Hour))
}

// evaluate runs the rebuild proof and both age checks. It returns the project's newest change time.
func (c *category) evaluate(env *core.Env, r core.Rule, p string) (time.Time, error) {
	if ctxDone(env) {
		return time.Time{}, core.Keep("budget", "run budget used up")
	}
	age := env.Settings.Age(c.name, r.MinAge)
	if _, err := os.Lstat(filepath.Join(p, "conda-meta")); err == nil {
		return time.Time{}, core.Keep("no-marker", "conda environment: "+p)
	}
	if n := filepath.Base(p); r.Kind == core.KindProject && (n == ".venv" || n == "venv") {
		if _, err := os.Lstat(filepath.Join(p, "pyvenv.cfg")); err != nil {
			return time.Time{}, core.Keep("no-marker", "no pyvenv.cfg in "+n)
		}
	}
	if r.Kind == core.KindFile {
		if err := fileProof(env, p, r.Markers); err != nil {
			return time.Time{}, err
		}
	} else if err := guard.ProjectArtifact(env.Run, p, r.Markers); err != nil {
		return time.Time{}, err
	}
	newest := projectNewest(filepath.Dir(p), p, artefactNames(env))
	if d := env.Now.Sub(newest); d < age {
		return newest, core.Keep("young", fmt.Sprintf("project changed %s ago", d.Round(time.Second)))
	}
	if err := guard.Fresh(p, age, env.Now); err != nil {
		return newest, err
	}
	return newest, nil
}

// projectNewest is the newest change inside parent down to depth 2, without exclude, without .git (a fetch
// or gc is not the user working) and without sibling artefact directories. Regular files count their ctime
// too; directories their mtime.
func projectNewest(parent, exclude string, artefacts []string) time.Time {
	var newest time.Time
	note := func(t time.Time) {
		if t.After(newest) {
			newest = t
		}
	}
	var visit func(dir string, depth int)
	visit = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			// Artefact names count as artefacts only directly in the marker directory: src/build is source.
			if p == exclude || (e.IsDir() && (e.Name() == ".git" || (depth == 1 && matches(artefacts, e.Name())))) {
				continue
			}
			if e.IsDir() {
				if fi, err := e.Info(); err == nil {
					note(fi.ModTime())
				}
				if depth < 2 {
					visit(p, depth+1)
				}
				continue
			}
			if t, err := guard.Newest(p); err == nil {
				note(t)
			}
		}
	}
	visit(parent, 1)
	return newest
}

// fileProof is S5 for a single file: a marker next to it, inside a git work tree, git-ignored, not tracked.
func fileProof(env *core.Env, p string, markers []string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	if !fi.Mode().IsRegular() {
		return core.Keep("not-ignored", p+" is not a regular file")
	}
	parent, base := filepath.Dir(p), filepath.Base(p)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	found := false
	for _, e := range entries {
		if matches(markers, e.Name()) {
			found = true
			break
		}
	}
	if !found {
		return core.Keep("no-marker", parent)
	}
	if _, err := os.Lstat(filepath.Join(parent, "HEAD")); err == nil {
		if oi, err := os.Lstat(filepath.Join(parent, "objects")); err == nil && oi.IsDir() {
			return core.Keep("nested-git", "a bare repository holds "+base)
		}
	}
	git := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		out, err := env.Run.Run(ctx, parent, guard.GitArgv(parent, args...)...)
		return string(out), err
	}
	out, err := git("rev-parse", "--is-inside-work-tree")
	if err != nil {
		if exitCodeOf(err) == 128 {
			return core.Keep("not-ignored", "not inside a git work tree")
		}
		return core.Keep("gitfail", err.Error())
	}
	if strings.TrimSpace(out) != "true" {
		return core.Keep("not-ignored", "not inside a git work tree")
	}
	out, err = git("rev-parse", "--git-dir", "--git-common-dir")
	if err != nil {
		return core.Keep("gitfail", err.Error())
	}
	dirs := strings.Split(strings.TrimSpace(out), "\n")
	if len(dirs) != 2 {
		return core.Keep("gitfail", "unexpected rev-parse output")
	}
	if realGitDir(parent, dirs[0]) != realGitDir(parent, dirs[1]) {
		return core.Keep("not-ignored", "inside a linked worktree")
	}
	out, err = git("ls-files", "--", base)
	if err != nil {
		return core.Keep("gitfail", err.Error())
	}
	if strings.TrimSpace(out) != "" {
		return core.Keep("tracked", base+" is tracked")
	}
	if _, err = git("check-ignore", "-q", "--", base); err != nil {
		if exitCodeOf(err) == 1 {
			return core.Keep("not-ignored", base+" is not git-ignored")
		}
		return core.Keep("gitfail", err.Error())
	}
	return nil
}

func realGitDir(parent, p string) string {
	p = strings.TrimSpace(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(parent, p)
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func exitCodeOf(err error) int {
	var ee *execx.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

// Proof re-runs the rebuild proof and the age checks right before removal.
func (c *category) Proof(env *core.Env, cand core.Candidate) error {
	for _, r := range c.rules(env) {
		if r.Key() != cand.RuleKey {
			continue
		}
		if !matches(r.Names, filepath.Base(cand.Path)) {
			return core.Keep("no-marker", "name does not match "+cand.RuleKey+": "+cand.Path)
		}
		_, err := c.evaluate(env, r, cand.Path)
		return err
	}
	return core.Keep("error", "no project rule "+cand.RuleKey)
}
