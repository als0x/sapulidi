// Package versions proposes old versions of toolchains (nvm, fnm, volta, pyenv, rustup, Go) and Playwright
// browsers, plus Xcode leftovers on macOS (spec 3.1, rules in packs/toolchains.toml, browsers.toml and
// apple.toml). Which version is active is read from files and from the tool's own answer, never by running a
// shell function; every doubt keeps every version of that location. They propose; the engine guards and runs.
package versions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/tools"
	"github.com/als0x/sapulidi/internal/tools/strategies"
)

func init() {
	core.RegisterToolAction("rustup", "uninstall")
	core.RegisterToolAction("xcrun", "simctl-delete-unavailable")
	for id := range locations {
		core.RegisterLocation(id)
	}
	for id := range appleLocations {
		core.RegisterLocation(id)
	}
	core.Register(&versionsCat{name: "toolchains"})
	core.Register(&versionsCat{name: "browsers"})
	core.Register(&appleCat{})
}

func ruleByKey(env *core.Env, category, key string) *core.Rule {
	for i := range env.Rules {
		if r := &env.Rules[i]; r.Category == category && r.Bucket != core.Never && r.Key() == key {
			return r
		}
	}
	return nil
}

// proof re-plans the rule right before removal (spec S8): the candidate must still be proposed.
func proof(env *core.Env, category string, c core.Candidate, plan func(*core.Env, *core.Rule) ([]core.Candidate, []core.Kept)) error {
	rule := ruleByKey(env, category, c.RuleKey)
	if rule == nil {
		return core.Keep("error", "no "+category+" rule for the candidate")
	}
	cands, kept := plan(env, rule)
	for _, k := range kept {
		if k.Path == c.Path && c.Path != "" {
			return core.Keep(k.Why, "no longer removable")
		}
	}
	for _, x := range cands {
		if x.Path == c.Path && x.Removal == c.Removal && slices.Equal(x.Command, c.Command) {
			return nil
		}
	}
	return core.Keep("no-marker", "no longer proposed")
}

// listDirs returns the names of the real directories (not symlinks) directly in dir, sorted.
func listDirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func youngerThan(env *core.Env, path string, age time.Duration) bool {
	fi, err := os.Lstat(path)
	return err != nil || env.Now.Sub(fi.ModTime()) < age
}

func ageText(age time.Duration) string {
	if age%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d days", int(age/(24*time.Hour)))
	}
	return age.String()
}

// ---- toolchains and browsers ----

type versionsCat struct{ name string }

func (c *versionsCat) Name() string      { return c.name }
func (*versionsCat) Bucket() core.Bucket { return core.Optional }

func (c *versionsCat) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	var cands []core.Candidate
	var kept []core.Kept
	for i := range env.Rules {
		r := &env.Rules[i]
		if r.Category != c.name || r.Bucket == core.Never || r.Kind != core.KindVersions {
			continue
		}
		if env.Ctx != nil && env.Ctx.Err() != nil {
			break
		}
		cs, ks := c.plan(env, r)
		cands, kept = append(cands, cs...), append(kept, ks...)
	}
	return cands, kept, nil
}

func (c *versionsCat) Proof(env *core.Env, cand core.Candidate) error {
	return proof(env, c.name, cand, c.plan)
}

func (c *versionsCat) plan(env *core.Env, rule *core.Rule) (cands []core.Candidate, kept []core.Kept) {
	find, ok := locations[rule.Location]
	if !ok {
		return nil, nil
	}
	f, ok := find(env)
	if !ok {
		return nil, nil
	}
	d, ok := strategies.Apply(rule.Strategy, strategies.Input{
		Versions: f.versions, Active: f.active, ActiveKnown: f.activeKnown, Pinned: f.pinned,
	})
	if !ok {
		return nil, nil
	}
	pathOf := map[string]string{}
	for _, v := range f.versions {
		pathOf[v.Name] = v.Path
	}
	for name, why := range d.Keep {
		kept = append(kept, core.Kept{Category: c.name, RuleKey: rule.Key(), Path: pathOf[name], Why: why})
	}
	age := env.Settings.Age(c.name, rule.MinAge)
	for _, v := range d.Remove {
		k := core.Kept{Category: c.name, RuleKey: rule.Key(), Path: v.Path}
		switch {
		case youngerThan(env, v.Path, age):
			k.Why = "young"
			kept = append(kept, k)
			continue
		case f.uninstall != nil && f.uninstall(v.Name) == nil:
			k.Why = "error"
			kept = append(kept, k)
			continue
		}
		cand := core.Candidate{
			Category: c.name, RuleKey: rule.Key(), Path: v.Path, Base: f.base, Removal: core.RenameDelete,
			Reason: fmt.Sprintf("%s %s: not the active or newest other version, untouched for %s", rule.Location, v.Name, ageText(age)),
		}
		if f.uninstall != nil {
			cand.Removal, cand.Command = core.ToolCommand, f.uninstall(v.Name)
		}
		cands = append(cands, cand)
	}
	slices.SortFunc(kept, func(a, b core.Kept) int { return compareStr(a.Path, b.Path) })
	return cands, kept
}

func compareStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// found is what a location learned about one installed set of versions.
type found struct {
	base        string // the directory that contains the version directories
	versions    []strategies.Version
	active      []string
	activeKnown bool
	pinned      []string
	uninstall   func(name string) []string // nil: the engine renames and deletes the directory
}

// pinMatches reports whether an installed name is the one a pin names: the pin is the whole name or its
// leading part up to a "." or "-" ("stable" for "stable-aarch64-apple-darwin", "go1.22" for "go1.22.3").
func pinMatches(name, pin string) bool {
	return pin != "" && (name == pin || len(name) > len(pin) && name[:len(pin)] == pin && (name[len(pin)] == '.' || name[len(pin)] == '-'))
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,100}$`)

// readRootFile returns the content of name at the top of every root that has it. A file that exists but cannot
// be read is an error: the pins are then unknown.
func readRootFile(env *core.Env, name string) ([]string, error) {
	var out []string
	for _, r := range env.Roots {
		b, err := os.ReadFile(filepath.Join(r.Path, name))
		switch {
		case err == nil:
			out = append(out, string(b))
		case os.IsNotExist(err):
		default:
			return nil, err
		}
	}
	return out, nil
}

// query asks the tool a read-only question; the first line of its answer.
func query(env *core.Env, argv ...string) (string, error) {
	out, err := tools.Query(env, argv...)
	if err != nil {
		return "", err
	}
	for _, line := range splitLines(out) {
		if line != "" {
			return line, nil
		}
	}
	return "", fmt.Errorf("%s: empty answer", argv[0])
}
