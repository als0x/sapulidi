// Package worktrees holds two categories (spec 3.1, rules in packs/worktrees.toml): worktrees, the optional
// removal of old linked worktrees with no unsaved work, and worktree-prune, the automatic `git worktree prune`
// of admin entries whose directory is gone. They propose; the engine guards and removes. Branches are never
// deleted, and a folder whose git admin entry is gone (an orphan) is only ever reported.
package worktrees

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/guard"
)

const (
	linkedLocation = "linked-worktrees"
	keptFactKey    = "worktrees-kept"
	orphanDepth    = 4
)

func init() {
	core.RegisterToolAction("git", "worktree-prune")
	core.RegisterLocation(linkedLocation)
	core.Register(&worktreesCat{})
	core.Register(&pruneCat{})
}

func ruleFor(env *core.Env, category string) *core.Rule {
	for i := range env.Rules {
		if r := &env.Rules[i]; r.Category == category && r.Bucket != core.Never {
			return r
		}
	}
	return nil
}

func ruleByKey(env *core.Env, category, key string) *core.Rule {
	for i := range env.Rules {
		if r := &env.Rules[i]; r.Category == category && r.Key() == key {
			return r
		}
	}
	return nil
}

// entry is one block of `git worktree list --porcelain -z`.
type entry struct {
	path                   string
	prunable, locked, bare bool
}

// parseList reads the NUL separated form, so a path that contains a newline stays whole.
func parseList(out string) []entry {
	var es []entry
	for l := range strings.SplitSeq(out, "\x00") {
		switch {
		case strings.HasPrefix(l, "worktree "):
			es = append(es, entry{path: strings.TrimPrefix(l, "worktree ")})
		case len(es) == 0:
		case l == "bare":
			es[len(es)-1].bare = true
		case l == "prunable" || strings.HasPrefix(l, "prunable "):
			es[len(es)-1].prunable = true
		case l == "locked" || strings.HasPrefix(l, "locked "):
			es[len(es)-1].locked = true
		}
	}
	return es
}

func listWorktrees(env *core.Env, dir string) ([]entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), guard.GitTimeout)
	defer cancel()
	out, err := env.Run.Run(ctx, dir, guard.GitArgv(dir, "worktree", "list", "--porcelain", "-z")...)
	if err != nil {
		return nil, err
	}
	return parseList(string(out)), nil
}

type rootInfo struct{ path, real string }

func rootsOf(env *core.Env) []rootInfo {
	var out []rootInfo
	for _, r := range env.Roots {
		real, err := filepath.EvalSymlinks(r.Path)
		if err != nil {
			real = r.Path
		}
		out = append(out, rootInfo{path: r.Path, real: real})
	}
	return out
}

// within maps a path git printed to the root's own spelling when it lies in the root. strict is true when the
// path is below the root and not the root itself.
func (ri rootInfo) within(p string) (mapped string, strict, ok bool) {
	for _, base := range []string{ri.real, ri.path} {
		if p == base {
			return ri.path, false, true
		}
		if rel, found := strings.CutPrefix(p, base+"/"); found {
			return filepath.Join(ri.path, rel), true, true
		}
	}
	return "", false, false
}

// repo is a main repository with its worktrees, seen from one or more roots.
type repo struct {
	main    string  // in the spelling of a root when it is inside one
	entries []entry // linked worktrees only, paths mapped like main
	roots   []rootInfo
}

// discover lists the worktrees of the main repository of every root. A root that is not a repository (a plain
// directory of repositories) yields nothing by design: the repositories under such a root are found because each
// one is its own discovered root. A root whose git call fails or times out has nothing for us either.
func discover(env *core.Env) []repo {
	roots := rootsOf(env)
	var repos []repo
	seen := map[string]bool{}
	for _, r := range env.Roots {
		if env.Ctx != nil && env.Ctx.Err() != nil {
			break
		}
		es, err := listWorktrees(env, r.Path)
		if err != nil || len(es) == 0 || es[0].bare {
			continue
		}
		mainGit := es[0].path
		if seen[mainGit] {
			continue
		}
		seen[mainGit] = true
		rp := repo{main: mainGit, roots: roots}
		for _, ri := range roots {
			if m, _, ok := ri.within(mainGit); ok {
				rp.main = m
				break
			}
		}
		for _, e := range es[1:] {
			e.path = rp.mapped(e.path)
			rp.entries = append(rp.entries, e)
		}
		repos = append(repos, rp)
	}
	return repos
}

func (rp repo) mapped(p string) string {
	for _, ri := range rp.roots {
		if m, _, ok := ri.within(p); ok {
			return m
		}
	}
	return p
}

// rootOf returns the longest root path that has p strictly below it.
func (rp repo) rootOf(p string) (string, bool) {
	best := ""
	for _, ri := range rp.roots {
		if strings.HasPrefix(p, ri.path+"/") && len(ri.path) > len(best) {
			best = ri.path
		}
	}
	return best, best != ""
}

// folderGone is the only proof an automatic prune accepts: the folder is really missing and the directory
// above it is there, so a volume that is just not mounted is never taken for a removed worktree.
func folderGone(p string) bool {
	if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	_, err := os.Stat(filepath.Dir(p))
	return err == nil
}

// prunable splits the entries `git worktree prune` would drop (locked ones it leaves alone) into those that
// pass folderGone and those that do not.
func prunable(es []entry) (safe, unsafe []entry) {
	for _, e := range es {
		if !e.prunable || e.locked {
			continue
		}
		if folderGone(e.path) {
			safe = append(safe, e)
		} else {
			unsafe = append(unsafe, e)
		}
	}
	return safe, unsafe
}

// orphanDir reports a directory whose .git file points at an admin directory that is gone.
func orphanDir(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return true
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	_, err = os.Stat(target)
	return err != nil
}

// findOrphans walks <root>/.worktrees (depth 4) for folders with a .git file whose admin dir is gone. It does
// not descend into a folder that has a .git of its own.
func findOrphans(env *core.Env, root string) []string {
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if env.Ctx != nil && env.Ctx.Err() != nil {
				return
			}
			if !e.IsDir() || e.Name() == "node_modules" {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if fi, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
				if fi.Mode().IsRegular() && orphanDir(p) {
					out = append(out, p)
				}
				continue
			}
			if depth < orphanDepth {
				walk(p, depth+1)
			}
		}
	}
	walk(filepath.Join(root, ".worktrees"), 1)
	return out
}

// ---- worktrees (optional) ----

type worktreesCat struct{}

func (*worktreesCat) Name() string        { return "worktrees" }
func (*worktreesCat) Bucket() core.Bucket { return core.Optional }

func keptFact(n int) string {
	if n == 1 {
		return "1 old worktree kept because of unsaved work"
	}
	return fmt.Sprintf("%d old worktrees kept because of unsaved work", n)
}

func (*worktreesCat) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	rule := ruleFor(env, "worktrees")
	if rule == nil {
		return nil, nil, nil
	}
	age := env.Settings.Age("worktrees", rule.MinAge)
	var cands []core.Candidate
	var kept []core.Kept
	unsaved := map[string]bool{} // by path, so one folder counts once
	done := func() ([]core.Candidate, []core.Kept, error) {
		// Told once per change: the store that backs Facts stays quiet while the text is the same.
		if len(unsaved) > 0 && env.Facts != nil {
			env.Facts.Fact(keptFactKey, keptFact(len(unsaved)))
		}
		return cands, kept, nil
	}
	for _, rp := range discover(env) {
		// Entries git calls prunable whose folder still exists (or whose volume is away) hold files the
		// prune category refuses to touch; they count here and are listed by that category.
		_, unsafe := prunable(rp.entries)
		for _, e := range unsafe {
			unsaved[e.path] = true
		}
		for _, e := range rp.entries {
			if env.Ctx != nil && env.Ctx.Err() != nil {
				return done()
			}
			if e.prunable && !e.locked || e.bare {
				continue
			}
			base, ok := rp.rootOf(e.path)
			if !ok {
				continue // outside every root, not ours
			}
			if e.locked {
				kept = append(kept, core.Kept{Category: "worktrees", RuleKey: rule.Key(), Path: e.path, Why: "locked"})
				unsaved[e.path] = true
				continue
			}
			err := guard.WorktreeBlocker(env.Run, e.path, env.Now, age)
			if err != nil {
				k := core.Kept{Category: "worktrees", RuleKey: rule.Key(), Path: e.path, Why: "error"}
				if ke, isKeep := err.(*core.KeepError); isKeep {
					k.Why = ke.Why
				}
				kept = append(kept, k)
				if slices.Contains([]string{"dirty", "untracked", "stash", "unpushed", "orphan"}, k.Why) {
					unsaved[e.path] = true
				}
				continue
			}
			cands = append(cands, core.Candidate{
				Category: "worktrees", RuleKey: rule.Key(), Path: e.path, Base: base, Removal: core.WorktreeRemove,
				Dir:    rp.main,
				Reason: "linked worktree with no unsaved work, last commit older than " + age.String(),
			})
		}
	}
	seen := map[string]bool{}
	for _, ri := range rootsOf(env) {
		if seen[ri.path] {
			continue
		}
		seen[ri.path] = true
		for _, p := range findOrphans(env, ri.path) {
			if unsaved[p] {
				continue
			}
			kept = append(kept, core.Kept{Category: "worktrees", RuleKey: rule.Key(), Path: p, Why: "orphan"})
			unsaved[p] = true
		}
	}
	return done()
}

// Proof re-runs the unsaved-work check (spec S9) right before `git worktree remove`.
func (*worktreesCat) Proof(env *core.Env, c core.Candidate) error {
	rule := ruleByKey(env, "worktrees", c.RuleKey)
	if rule == nil {
		return core.Keep("error", "no worktrees rule "+c.RuleKey)
	}
	if c.Dir == "" || c.Path == "" {
		return core.Keep("error", "worktree without path or main repository")
	}
	return guard.WorktreeBlocker(env.Run, c.Path, env.Now, env.Settings.Age("worktrees", rule.MinAge))
}

// ---- worktree-prune (auto) ----

type pruneCat struct{}

func (*pruneCat) Name() string        { return "worktree-prune" }
func (*pruneCat) Bucket() core.Bucket { return core.Auto }

func pruneCommand(main string) []string {
	return guard.GitArgv(main, "worktree", "prune")
}

func (*pruneCat) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	rule := ruleFor(env, "worktree-prune")
	if rule == nil {
		return nil, nil, nil
	}
	var cands []core.Candidate
	var kept []core.Kept
	reported := map[string]bool{}
	for _, rp := range discover(env) {
		safe, unsafe := prunable(rp.entries)
		// `git worktree prune` drops every prunable entry, so one doubtful entry stops the whole repository.
		if len(unsafe) > 0 {
			for _, e := range unsafe {
				reported[e.path] = true
				kept = append(kept, core.Kept{Category: "worktree-prune", RuleKey: rule.Key(), Path: e.path, Why: "orphan"})
			}
			continue
		}
		if len(safe) == 0 {
			continue
		}
		cands = append(cands, core.Candidate{
			Category: "worktree-prune", RuleKey: rule.Key(), Removal: core.ToolCommand,
			Command: pruneCommand(rp.main), Dir: rp.main,
			Reason: fmt.Sprintf("%d worktree admin entries of %s point at folders that are gone", len(safe), rp.main),
		})
	}
	// With the worktrees category on, its scan counts these entries in the same fact; reporting here too
	// would make the two counts overwrite each other.
	if len(reported) > 0 && env.Facts != nil && !env.Settings.CategoryEnabled("worktrees") {
		env.Facts.Fact(keptFactKey, keptFact(len(reported)))
	}
	return cands, kept, nil
}

// Proof re-checks that the command is the one this category builds and that every prunable entry of the
// repository still passes folderGone, with at least one left.
func (*pruneCat) Proof(env *core.Env, c core.Candidate) error {
	if ruleByKey(env, "worktree-prune", c.RuleKey) == nil {
		return core.Keep("error", "no worktree-prune rule "+c.RuleKey)
	}
	if c.Dir == "" || !slices.Equal(c.Command, pruneCommand(c.Dir)) {
		return core.Keep("error", "unexpected command")
	}
	es, err := listWorktrees(env, c.Dir)
	if err != nil {
		return core.Keep("gitfail", err.Error())
	}
	safe, unsafe := prunable(es)
	if len(unsafe) > 0 {
		return core.Keep("orphan", "a prunable entry still has a folder or a missing volume: "+unsafe[0].path)
	}
	if len(safe) == 0 {
		return core.Keep("no-marker", "nothing to prune in "+c.Dir)
	}
	return nil
}
