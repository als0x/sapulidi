// Package roots finds where the user works: configured roots plus the git repositories seen in
// agent history (spec 4.3). It never scans the home directory.
package roots

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/guard"
)

const (
	maxAge         = 180 * 24 * time.Hour
	maxLines       = 200
	maxBytes       = 1 << 20
	maxRoots       = 500
	maxPerFolder   = 3
	gitTimeout     = 5 * time.Second
	defaultBudget  = 20 * time.Second
	keptCategory   = "roots"
	sourceConfig   = "config"
	sourceClaude   = "claude"
	sourceCodex    = "codex"
	noCodexMessage = "(codex) no ~/.codex/sessions, discovery skipped"
)

// DiscoverEnv carries what discovery needs; nothing here is read from the real machine.
type DiscoverEnv struct {
	Home     string
	Config   *config.Config
	Platform core.Platform
	Run      core.Runner
	Now      time.Time
	// TempDirs are temp locations (callers pass Platform.TempDir(), /tmp and /private/tmp). A root that
	// is one of them is refused; a root found in agent history under one of them is dropped.
	TempDirs []string
	// Budget bounds the whole discovery; zero means 20 seconds.
	Budget time.Duration
}

type cand struct {
	path, source, key string
}

// Discover returns the roots sorted by path and the candidates it dropped, each with a reason token:
// deny, owner, device, missing, not-git, temp, nested, cap, budget. A Kept with Why "no-codex" says
// Codex discovery was skipped (core.Kept has no detail field, so the sentence is in Path).
func Discover(env DiscoverEnv) ([]core.Root, []core.Kept) {
	budget := env.Budget
	if budget <= 0 {
		budget = defaultBudget
	}
	deadline := time.Now().Add(budget)
	expired := false
	over := func() bool {
		if !expired && time.Now().After(deadline) {
			expired = true
		}
		return expired
	}
	darwin := env.Platform != nil && env.Platform.OS() == "darwin"
	keyOf := func(p string) string {
		k := realPath(p)
		if darwin {
			k = strings.ToLower(k)
		}
		return k
	}

	var kept []core.Kept
	drop := func(path, why string) {
		kept = append(kept, core.Kept{Category: keptCategory, Path: path, Why: why})
	}

	var cands []cand
	if env.Config != nil {
		for _, p := range env.Config.Roots {
			cands = append(cands, cand{path: filepath.Clean(p), source: sourceConfig})
		}
	}

	// Agent history: cwd values, first sighting wins.
	cwds := map[string]string{}
	var order []string
	add := func(cwd, source string) {
		if _, ok := cwds[cwd]; !ok {
			cwds[cwd] = source
			order = append(order, cwd)
		}
	}
	cutoff := env.Now.Add(-maxAge)

	for _, files := range claudeFolders(env.Home, cutoff) {
		if over() {
			break
		}
		for i, f := range files {
			if i == maxPerFolder || over() {
				break
			}
			if cwd, ok := firstCwd(f, false); ok {
				add(cwd, sourceClaude)
				break
			}
		}
	}

	codexDir := filepath.Join(env.Home, ".codex", "sessions")
	if fi, err := os.Stat(codexDir); err != nil || !fi.IsDir() {
		kept = append(kept, core.Kept{Category: keptCategory, Path: noCodexMessage, Why: "no-codex"})
	} else {
		var files []string
		_ = filepath.WalkDir(codexDir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() && strings.HasSuffix(p, ".jsonl") && recent(p, cutoff) {
				files = append(files, p)
			}
			return nil
		})
		sort.Strings(files)
		for _, f := range files {
			if over() {
				break
			}
			if cwd, ok := firstCwd(f, true); ok {
				add(cwd, sourceCodex)
			}
		}
	}

	var tops []string // real paths of toplevels already resolved
	for _, cwd := range order {
		if over() {
			break
		}
		if !filepath.IsAbs(cwd) {
			drop(cwd, "not-git")
			continue
		}
		cwd = filepath.Clean(cwd)
		rc := realPath(cwd)
		if underAny(rc, tops) {
			continue
		}
		top, why := repoRoot(env.Run, cwd)
		if why != "" {
			drop(cwd, why)
			continue
		}
		tops = append(tops, realPath(top))
		cands = append(cands, cand{path: top, source: cwds[cwd]})
	}
	if expired {
		drop("", "budget")
	}

	temps := make([]string, 0, len(env.TempDirs))
	for _, t := range env.TempDirs {
		if t != "" {
			temps = append(temps, realPath(t))
		}
	}

	seen := map[string]bool{}
	var cfg, disc []cand
	for _, c := range cands {
		c.key = keyOf(c.path)
		if seen[c.key] {
			continue
		}
		seen[c.key] = true
		if why := check(env, c, temps); why != "" {
			drop(c.path, why)
			continue
		}
		if c.source == sourceConfig {
			cfg = append(cfg, c)
		} else {
			disc = append(disc, c)
		}
	}
	sort.Slice(cfg, func(i, j int) bool { return cfg[i].path < cfg[j].path })
	sort.Slice(disc, func(i, j int) bool { return disc[i].path < disc[j].path })

	// Config roots win: they are only dropped when inside another config root; a discovered root
	// inside any kept root is dropped.
	var final []cand
	for _, c := range cfg {
		if insideAny(c.key, final) {
			drop(c.path, "nested")
			continue
		}
		final = append(final, c)
	}
	for _, c := range disc {
		if insideAny(c.key, final) {
			drop(c.path, "nested")
			continue
		}
		final = append(final, c)
	}

	// Cap: config roots first, then discovered ones in path order.
	if len(final) > maxRoots {
		for _, c := range final[maxRoots:] {
			drop(c.path, "cap")
		}
		final = final[:maxRoots]
	}

	sort.Slice(final, func(i, j int) bool { return final[i].path < final[j].path })
	out := make([]core.Root, 0, len(final))
	for _, c := range final {
		out = append(out, core.Root{Path: c.path, Source: c.source})
	}
	return out, kept
}

func check(env DiscoverEnv, c cand, temps []string) string {
	fi, err := os.Stat(c.path)
	if err != nil || !fi.IsDir() {
		return "missing"
	}
	if guard.DenyRoot(env.Home, c.path) != nil {
		return "deny"
	}
	rp := realPath(c.path)
	for _, t := range temps {
		if rp == t || (c.source != sourceConfig && under(rp, t)) {
			return "temp"
		}
	}
	if owned, err := env.Platform.OwnedByUser(c.path); err != nil || !owned {
		return "owner"
	}
	if c.source != sourceConfig {
		if same, err := env.Platform.SameDevice(env.Home, c.path); err != nil || !same {
			return "device"
		}
	}
	return ""
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

// under reports whether p is strictly inside dir.
func under(p, dir string) bool {
	return strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if p == d || under(p, d) {
			return true
		}
	}
	return false
}

func insideAny(key string, roots []cand) bool {
	for _, r := range roots {
		if under(key, r.key) {
			return true
		}
	}
	return false
}

func recent(path string, cutoff time.Time) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular() && fi.ModTime().After(cutoff)
}

// claudeFolders returns the recent transcripts of each project folder, newest first, folders in
// name order. Only stat is used here; no file is opened.
func claudeFolders(home string, cutoff time.Time) [][]string {
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	type file struct {
		path string
		mod  time.Time
	}
	byDir := map[string][]file{}
	for _, m := range matches {
		fi, err := os.Lstat(m)
		if err != nil || !fi.Mode().IsRegular() || !fi.ModTime().After(cutoff) {
			continue
		}
		d := filepath.Dir(m)
		byDir[d] = append(byDir[d], file{m, fi.ModTime()})
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	out := make([][]string, 0, len(dirs))
	for _, d := range dirs {
		fl := byDir[d]
		sort.Slice(fl, func(i, j int) bool { return fl[i].mod.After(fl[j].mod) })
		paths := make([]string, len(fl))
		for i, f := range fl {
			paths[i] = f.path
		}
		out = append(out, paths)
	}
	return out
}

// repoRoot maps an absolute cwd to the top-level of its repository, and only a linked worktree to its
// main repository. The second result is "" on success, otherwise "missing" or "not-git".
func repoRoot(run core.Runner, cwd string) (string, string) {
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return "", "missing"
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := run.Run(ctx, cwd, guard.GitArgv(cwd, "rev-parse", "--show-toplevel", "--git-dir", "--git-common-dir")...)
	if err != nil {
		return "", "not-git"
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 3 || lines[0] == "" {
		return "", "not-git"
	}
	abs := func(p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		return filepath.Clean(p)
	}
	top, gitDir, common := filepath.Clean(lines[0]), abs(lines[1]), abs(lines[2])
	if realPath(gitDir) != realPath(common) && filepath.Base(common) == ".git" {
		top = filepath.Dir(common)
	}
	return top, ""
}

// firstCwd reads lines until the first object with a string cwd (top level; for Codex also
// payload.cwd). It stops after maxLines lines or maxBytes bytes and keeps no other field.
func firstCwd(path string, codex bool) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	r := bufio.NewReader(io.LimitReader(f, maxBytes))
	for range maxLines {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var v struct {
				Cwd     any `json:"cwd"`
				Payload struct {
					Cwd any `json:"cwd"`
				} `json:"payload"`
			}
			// a truncated final line fails to decode and is ignored
			if json.Unmarshal(line, &v) == nil {
				if s, ok := v.Cwd.(string); ok && s != "" {
					return s, true
				}
				if s, ok := v.Payload.Cwd.(string); ok && codex && s != "" {
					return s, true
				}
			}
		}
		if err != nil {
			break
		}
	}
	return "", false
}
