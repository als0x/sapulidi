package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/guard"
)

func keepWhy(err error) string {
	if ke, ok := err.(*core.KeepError); ok {
		return ke.Why
	}
	return "error"
}

// scratch is agent-scratch: Claude Code's per-session folders `<base>/<slug>/<session-id>` and Codex's
// `~/.codex/tmp/*` (spec 3.1, 4.2). Only depth exactly 2 under a Claude base is a candidate, and only below a
// slug that starts with "-"; siblings at depth 1 hold the host's own internals and are never touched.
type scratch struct{}

func (*scratch) Name() string        { return "agent-scratch" }
func (*scratch) Bucket() core.Bucket { return core.Auto }

func codexBase(env *core.Env) string { return filepath.Join(env.Home, ".codex", "tmp") }

func isCodexBase(env *core.Env, base string) bool {
	cb := codexBase(env)
	return base == cb || normalize(base) == normalize(cb)
}

var sessionID = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

// transcriptActive reports whether a Claude transcript of the session was modified within window. Anything it
// cannot read counts as active, so a failure keeps scratch instead of removing it. Project folders may be
// symlinks (a moved or shared project), so the lookup follows them.
func transcriptActive(env *core.Env, id string, window time.Duration) bool {
	projects := filepath.Join(claudeDir(env), "projects")
	list, err := os.ReadDir(projects)
	if err != nil {
		return !errors.Is(err, fs.ErrNotExist)
	}
	for _, e := range list {
		fi, err := os.Stat(filepath.Join(projects, e.Name(), id+".jsonl"))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
				return true
			}
			continue
		}
		if env.Now.Sub(fi.ModTime()) < window {
			return true
		}
	}
	return false
}

func (c *scratch) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	var cands []core.Candidate
	var kept []core.Kept
	bases := env.Platform.ScratchBases()
	for _, rule := range rulesAt(env, "agent-scratch", locClaudeScratch) {
		for _, base := range bases {
			if isCodexBase(env, base) {
				continue
			}
			for _, slug := range realDirs(base) {
				if !strings.HasPrefix(filepath.Base(slug), "-") {
					continue
				}
				for _, sess := range realDirs(slug) {
					if cancelled(env) {
						return cands, kept, nil
					}
					keep := func(why string) {
						kept = append(kept, core.Kept{Category: "agent-scratch", RuleKey: rule.Key(), Path: sess, Why: why})
					}
					if !sessionID.MatchString(filepath.Base(sess)) {
						keep("no-marker")
						continue
					}
					if transcriptActive(env, filepath.Base(sess), rule.MinAge) {
						keep("active")
						continue
					}
					if oldEnough(env, sess, rule.MinAge) {
						if err := repoKeep(env, sess); err != nil {
							keep(keepWhy(err))
							continue
						}
					}
					if fi, err := os.Lstat(sess); err != nil || env.Now.Sub(fi.ModTime()) < rule.MinAge {
						keep("young")
						continue
					}
					cands = append(cands, core.Candidate{
						Category: "agent-scratch", RuleKey: rule.Key(), Path: sess, Base: slug, Removal: core.RenameDelete,
						Reason: "Claude Code scratch of a session without a transcript change for " + rule.MinAge.String(),
					})
				}
			}
		}
	}
	for _, rule := range rulesAt(env, "agent-scratch", locCodexTmp) {
		for _, base := range bases {
			if !isCodexBase(env, base) {
				continue
			}
			for _, p := range entries(base, func(fi os.FileInfo) bool { return fi.Mode()&os.ModeSymlink == 0 }) {
				if cancelled(env) {
					return cands, kept, nil
				}
				if filepath.Base(p) != "arg0" && oldEnough(env, p, rule.MinAge) {
					if err := repoKeep(env, p); err != nil {
						kept = append(kept, core.Kept{Category: "agent-scratch", RuleKey: rule.Key(), Path: p, Why: keepWhy(err)})
						continue
					}
				}
				if filepath.Base(p) == "arg0" {
					kept = append(kept, core.Kept{Category: "agent-scratch", RuleKey: rule.Key(), Path: p, Why: "process"})
					continue
				}
				cands = append(cands, core.Candidate{
					Category: "agent-scratch", RuleKey: rule.Key(), Path: p, Base: base, Removal: core.RenameDelete,
					Reason: "Codex scratch untouched for " + rule.MinAge.String(),
				})
			}
		}
	}
	return cands, kept, nil
}

// Proof re-checks the keep rule: depth, name, kind, and for Claude that no transcript changed meanwhile.
func (s *scratch) Proof(env *core.Env, c core.Candidate) error {
	if err := s.proofNames(env, c); err != nil {
		return err
	}
	return repoKeep(env, c.Path)
}

// repoKeep keeps a scratch entry that holds a git repository anywhere inside it: the category is auto and an
// agent's scratch directory is a natural place for a clone, so names and age alone are no proof (spec S5). A walk
// error, a cancelled run included, keeps too.
func repoKeep(env *core.Env, p string) error {
	held, err := guard.HoldsRepo(env.Ctx, p)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	if held {
		return core.Keep("nested-git", "a git repository inside "+p)
	}
	return nil
}

// oldEnough is the cheap age gate before the walk for a repository: only an entry the freshness guard can pass is
// walked.
func oldEnough(env *core.Env, p string, age time.Duration) bool {
	fi, err := os.Lstat(p)
	return err == nil && env.Now.Sub(fi.ModTime()) >= age
}

func (*scratch) proofNames(env *core.Env, c core.Candidate) error {
	rule, ok := ruleByKey(env, "agent-scratch", c.RuleKey)
	if !ok {
		return core.Keep("error", "no agent-scratch rule "+c.RuleKey)
	}
	if filepath.Dir(c.Path) != c.Base {
		return core.Keep("place", "not directly inside its base: "+c.Path)
	}
	fi, err := os.Lstat(c.Path)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return core.Keep("place", "now a symlink: "+c.Path)
	}
	switch rule.Location {
	case locCodexTmp:
		if !isCodexBase(env, c.Base) {
			return core.Keep("place", "not the Codex scratch base: "+c.Base)
		}
		if filepath.Base(c.Path) == "arg0" {
			return core.Keep("process", "arg0 holds helper programs in use")
		}
	case locClaudeScratch:
		if !fi.IsDir() {
			return core.Keep("place", "not a directory: "+c.Path)
		}
		if !sessionID.MatchString(filepath.Base(c.Path)) {
			return core.Keep("no-marker", "not a session id: "+c.Path)
		}
		if !strings.HasPrefix(filepath.Base(c.Base), "-") {
			return core.Keep("no-marker", "slug does not start with -: "+c.Base)
		}
		isBase := false
		for _, b := range env.Platform.ScratchBases() {
			if b == filepath.Dir(c.Base) && !isCodexBase(env, b) {
				isBase = true
			}
		}
		if !isBase {
			return core.Keep("place", "not under a Claude scratch base: "+c.Base)
		}
		if transcriptActive(env, filepath.Base(c.Path), rule.MinAge) {
			return core.Keep("active", "transcript of the session changed within "+rule.MinAge.String())
		}
	default:
		return core.Keep("error", "rule "+c.RuleKey+" is not a scratch location")
	}
	return nil
}
