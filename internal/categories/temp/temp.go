// Package temp is the category of build and test leftovers that tools write directly into the user's temp
// directory (spec 3.1, rules in packs/temp.toml). It proposes; the engine guards and removes.
package temp

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/guard"
)

type category struct {
	// extra returns bases scanned besides env.TempDir.
	extra func(env *core.Env) []string
}

func init() { core.Register(&category{extra: linuxTmp}) }

// linuxTmp adds /tmp on Linux, where the user temp dir can differ from it. It adds nothing on macOS (the
// agent scratch category owns /private/tmp) and nothing under the test flag, so a test run never reads the
// real /tmp.
func linuxTmp(env *core.Env) []string {
	if os.Getenv("SAPULIDI_TEST") == "1" || env.Platform == nil || env.Platform.OS() != "linux" {
		return nil
	}
	return []string{"/tmp"}
}

func (*category) Name() string        { return "temp" }
func (*category) Bucket() core.Bucket { return core.Auto }

func tempRules(env *core.Env) []core.Rule {
	var out []core.Rule
	for _, r := range env.Rules {
		if r.Category == "temp" && r.Kind == core.KindTemp && r.Bucket != core.Never {
			out = append(out, r)
		}
	}
	return out
}

func matches(r core.Rule, name string) bool {
	for _, p := range r.Names {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

func (c *category) bases(env *core.Env) []string {
	var out []string
	seen := map[string]bool{}
	add := func(b string) {
		if b != "" && b != "/" && filepath.IsAbs(b) && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	add(env.TempDir)
	if c.extra != nil {
		for _, b := range c.extra(env) {
			add(b)
		}
	}
	return out
}

// Scan lists the entries directly inside the temp dir whose name a temp rule matches. Entries owned by someone
// else and symlinks are not ours and are skipped without a word.
func (c *category) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	rules := tempRules(env)
	if len(rules) == 0 {
		return nil, nil, nil
	}
	var cands []core.Candidate
	var kept []core.Kept
	for _, base := range c.bases(env) {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue // a missing or unreadable temp dir has nothing for us
		}
		for _, e := range entries {
			if env.Ctx != nil && env.Ctx.Err() != nil {
				return cands, kept, nil
			}
			var rule *core.Rule
			for i := range rules {
				if matches(rules[i], e.Name()) {
					rule = &rules[i]
					break
				}
			}
			if rule == nil {
				continue
			}
			p := filepath.Join(base, e.Name())
			fi, err := os.Lstat(p)
			if err != nil || !fi.IsDir() { // every rule names a directory; a symlink is not one here
				continue
			}
			owned, err := env.Platform.OwnedByUser(p)
			if err != nil {
				kept = append(kept, core.Kept{Category: "temp", RuleKey: rule.Key(), Path: p, Why: "error"})
				continue
			}
			if !owned {
				continue
			}
			// Only an entry old enough to be removed is walked for a repository; a young one is kept by the
			// engine's freshness guard anyway.
			if env.Now.Sub(fi.ModTime()) >= rule.MinAge {
				if err := repoGuard(env.Ctx, p); err != nil {
					kept = append(kept, core.Kept{Category: "temp", RuleKey: rule.Key(), Path: p, Why: keepWhy(err)})
					continue
				}
			}
			cands = append(cands, core.Candidate{
				Category: "temp", RuleKey: rule.Key(), Path: p, Base: base, Removal: core.RenameDelete,
				Reason: "temp leftover of " + rule.Pack + "/" + rule.ID + ", untouched for " + rule.MinAge.String(),
			})
		}
	}
	return cands, kept, nil
}

// Proof re-checks right before removal that the entry still is what the rule names: its name matches, it sits
// directly in its base, it is not a symlink and it is still owned by the user.
func (*category) Proof(env *core.Env, c core.Candidate) error {
	var rule *core.Rule
	for _, r := range tempRules(env) {
		if r.Key() == c.RuleKey {
			r := r
			rule = &r
			break
		}
	}
	if rule == nil {
		return core.Keep("error", "no temp rule "+c.RuleKey)
	}
	if filepath.Dir(c.Path) != c.Base || !matches(*rule, filepath.Base(c.Path)) {
		return core.Keep("no-marker", "name does not match "+c.RuleKey+": "+c.Path)
	}
	fi, err := os.Lstat(c.Path)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	if !fi.IsDir() {
		return core.Keep("place", "not a directory (or now a symlink): "+c.Path)
	}
	owned, err := env.Platform.OwnedByUser(c.Path)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	if !owned {
		return core.Keep("place", "not owned by the user: "+c.Path)
	}
	return repoGuard(env.Ctx, c.Path)
}

// repoGuard keeps an entry that holds a git repository anywhere inside it: no temp tool writes .git, so it is
// somebody's work (spec S5). A walk error keeps too.
func repoGuard(ctx context.Context, p string) error {
	held, err := guard.HoldsRepo(ctx, p)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	if held {
		return core.Keep("nested-git", "a git repository inside "+p)
	}
	return nil
}

func keepWhy(err error) string {
	var k *core.KeepError
	if errors.As(err, &k) {
		return k.Why
	}
	return "error"
}
