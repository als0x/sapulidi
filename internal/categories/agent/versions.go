package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
)

// versions is agent-versions: old Claude Code CLI versions and old plugin versions in the plugin cache. Only
// entries named like a semantic version are ever offered; every other name (commit hashes, partial downloads,
// locks) is kept. Of the real versions, the ones in use and the highest other one are kept. When "in use" cannot
// be determined, nothing is proposed.
type versions struct{}

func (*versions) Name() string        { return "agent-versions" }
func (*versions) Bucket() core.Bucket { return core.Optional }

// item is something to remove and the exact parent it sits in.
type item struct{ path, base string }

func notSymlink(fi os.FileInfo) bool { return fi.Mode()&os.ModeSymlink == 0 }

var versionRe = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.]+))?$`)

type semver struct {
	n   [3]int
	pre string
}

func parseVersion(name string) (semver, bool) {
	m := versionRe.FindStringSubmatch(name)
	if m == nil {
		return semver{}, false
	}
	v := semver{pre: m[4]}
	for i := range v.n {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return semver{}, false
		}
		v.n[i] = n
	}
	return v, true
}

// less orders by the numeric parts; a pre-release sorts below the same release.
func (a semver) less(b semver) bool {
	for i := range a.n {
		if a.n[i] != b.n[i] {
			return a.n[i] < b.n[i]
		}
	}
	switch {
	case a.pre == b.pre, a.pre == "":
		return false
	case b.pre == "":
		return true
	}
	return a.pre < b.pre
}

// realVersions keeps the paths whose base name is a semantic version.
func realVersions(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, ok := parseVersion(filepath.Base(p)); ok {
			out = append(out, p)
		}
	}
	return out
}

// highest returns the path with the highest version, ties broken by name; "" when there is none.
func highest(paths []string) string {
	best, bestV := "", semver{}
	for _, p := range paths {
		v, ok := parseVersion(filepath.Base(p))
		if !ok {
			continue
		}
		if best == "" || bestV.less(v) || (!v.less(bestV) && p > best) {
			best, bestV = p, v
		}
	}
	return best
}

// dropHighest returns paths without the highest one.
func dropHighest(paths []string) []string {
	h := highest(paths)
	var out []string
	for _, p := range paths {
		if p != h {
			out = append(out, p)
		}
	}
	return out
}

// activeClaude returns every entry of dir that ~/.local/bin/claude and `claude` on the PATH resolve to.
func activeClaude(env *core.Env, dir string) map[string]bool {
	targets := []string{filepath.Join(env.Home, ".local", "bin", "claude")}
	if env.Run != nil {
		if p, err := env.Run.LookPath("claude"); err == nil {
			targets = append(targets, p)
		}
	}
	realDir := normalize(dir)
	out := map[string]bool{}
	for _, t := range targets {
		r, err := filepath.EvalSymlinks(t)
		if err != nil || !below(realDir, r) {
			continue
		}
		first, _, _ := strings.Cut(r[len(realDir)+1:], string(filepath.Separator))
		out[filepath.Join(dir, first)] = true
	}
	return out
}

// claudeItems lists the CLI versions to remove: all but the active ones and the highest other one.
func claudeItems(env *core.Env) []item {
	dir := filepath.Join(env.Home, ".local", "share", "claude", "versions")
	active := activeClaude(env, dir)
	if len(active) == 0 {
		return nil
	}
	var others []string
	for _, p := range realVersions(entries(dir, notSymlink)) {
		if !active[p] {
			others = append(others, p)
		}
	}
	var out []item
	for _, p := range dropHighest(others) {
		out = append(out, item{p, dir})
	}
	return out
}

// installed is one entry of installed_plugins.json.
type installed struct {
	key, path, version string
}

// installedEntries reads installed_plugins.json. ok is false when the file is unreadable, is not JSON or names
// no install path at all.
func installedEntries(env *core.Env) (list []installed, ok bool) {
	data, err := os.ReadFile(filepath.Join(claudeDir(env), "plugins", "installed_plugins.json"))
	if err != nil {
		return nil, false
	}
	var doc struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
			Version     string `json:"version"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil, false
	}
	withPath := false
	for key, es := range doc.Plugins {
		for _, e := range es {
			list = append(list, installed{key, e.InstallPath, e.Version})
			withPath = withPath || e.InstallPath != ""
		}
	}
	return list, withPath
}

// pluginItems lists the plugin versions to remove: per plugin, among the real versions, all but the installed
// ones and the highest other one. An installed entry that matches no directory of its plugin keeps the whole
// plugin.
func pluginItems(env *core.Env) []item {
	cache := filepath.Join(claudeDir(env), "plugins", "cache")
	list, ok := installedEntries(env)
	if !ok {
		return nil
	}
	var out []item
	for _, mkt := range realDirs(cache) {
		for _, plugin := range realDirs(mkt) {
			dirs := realDirs(plugin)
			realPlugin := normalize(plugin)
			keep := map[string]bool{}
			keepAll := false
			for _, e := range list {
				name, m, _ := strings.Cut(e.key, "@")
				belongs := normalize(filepath.Join(cache, m, name)) == realPlugin ||
					(e.path != "" && filepath.Dir(normalize(e.path)) == realPlugin)
				if !belongs {
					continue
				}
				matched := false
				for _, d := range dirs {
					if (e.path != "" && normalize(d) == normalize(e.path)) || (e.version != "" && filepath.Base(d) == e.version) {
						keep[d], matched = true, true
					}
				}
				if !matched {
					keepAll = true
				}
			}
			if keepAll {
				continue
			}
			var others []string
			for _, v := range realVersions(dirs) {
				if !keep[v] {
					others = append(others, v)
				}
			}
			for _, v := range dropHighest(others) {
				out = append(out, item{v, plugin})
			}
		}
	}
	return out
}

func (*versions) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	var cands []core.Candidate
	add := func(rule core.Rule, items []item, reason string) {
		for _, it := range items {
			cands = append(cands, core.Candidate{
				Category: "agent-versions", RuleKey: rule.Key(), Path: it.path, Base: it.base, Removal: core.RenameDelete, Reason: reason,
			})
		}
	}
	for _, rule := range rulesAt(env, "agent-versions", locClaudeVersions) {
		add(rule, claudeItems(env), "Claude Code version that is neither active nor the highest other one")
	}
	for _, rule := range rulesAt(env, "agent-versions", locClaudePluginCache) {
		add(rule, pluginItems(env), "plugin version that is neither installed nor the highest other one")
	}
	return cands, nil, nil
}

// Proof recomputes the keep set: a candidate that became active, installed or the highest other is kept.
func (*versions) Proof(env *core.Env, c core.Candidate) error {
	rule, ok := ruleByKey(env, "agent-versions", c.RuleKey)
	if !ok {
		return core.Keep("error", "no agent-versions rule "+c.RuleKey)
	}
	if filepath.Dir(c.Path) != c.Base {
		return core.Keep("place", "not directly inside its base: "+c.Path)
	}
	if fi, err := os.Lstat(c.Path); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		return core.Keep("place", "missing or a symlink: "+c.Path)
	}
	var items []item
	switch rule.Location {
	case locClaudeVersions:
		items = claudeItems(env)
	case locClaudePluginCache:
		items = pluginItems(env)
	default:
		return core.Keep("error", "rule "+c.RuleKey+" is not a versions location")
	}
	for _, it := range items {
		if it.path == c.Path && it.base == c.Base {
			return nil
		}
	}
	return core.Keep("active", "version is in use or the highest other one: "+c.Path)
}
