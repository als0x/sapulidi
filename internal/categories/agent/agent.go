// Package agent holds the categories of what coding agents leave behind: per-session scratch folders
// (agent-scratch), old transcripts (agent-transcripts) and old versions of agent CLIs and plugin caches
// (agent-versions). Rules live in packs/agent.toml. The categories propose; the engine guards and removes.
package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
)

const (
	locClaudeScratch     = "claude-scratch"
	locCodexTmp          = "codex-tmp"
	locClaudeTranscripts = "claude-transcripts"
	locCodexSessions     = "codex-sessions"
	locClaudeVersions    = "claude-versions"
	locClaudePluginCache = "claude-plugin-cache"
)

func init() {
	for _, l := range []string{locClaudeScratch, locCodexTmp, locClaudeTranscripts, locCodexSessions,
		locClaudeVersions, locClaudePluginCache} {
		core.RegisterLocation(l)
	}
	core.Register(&scratch{})
	core.Register(&transcripts{})
	core.Register(&versions{})
}

// rulesAt returns the path rules of one category for one location.
func rulesAt(env *core.Env, category, location string) []core.Rule {
	var out []core.Rule
	for _, r := range env.Rules {
		if r.Category == category && r.Kind == core.KindPath && r.Location == location && r.Bucket != core.Never {
			out = append(out, r)
		}
	}
	return out
}

// ruleByKey finds the rule a candidate came from, for its category.
func ruleByKey(env *core.Env, category, key string) (core.Rule, bool) {
	for _, r := range env.Rules {
		if r.Category == category && r.Kind == core.KindPath && r.Key() == key {
			return r, true
		}
	}
	return core.Rule{}, false
}

func cancelled(env *core.Env) bool { return env.Ctx != nil && env.Ctx.Err() != nil }

func below(base, p string) bool { return strings.HasPrefix(p, base+string(filepath.Separator)) }

// realDirs lists the entries of dir that are real directories (symlinks are not ours).
func realDirs(dir string) []string {
	return entries(dir, func(fi os.FileInfo) bool { return fi.IsDir() })
}

// entries lists the full paths directly inside dir whose Lstat info passes keep.
func entries(dir string, keep func(os.FileInfo) bool) []string {
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range list {
		p := filepath.Join(dir, e.Name())
		if fi, err := os.Lstat(p); err == nil && keep(fi) {
			out = append(out, p)
		}
	}
	return out
}

// claudeDir is Claude Code's config directory: CLAUDE_CONFIG_DIR when set, else ~/.claude.
func claudeDir(env *core.Env) string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(d) {
		return filepath.Clean(d)
	}
	return filepath.Join(env.Home, ".claude")
}

// normalize resolves symlinks when the path exists and cleans it otherwise.
func normalize(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
