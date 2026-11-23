// Package caches holds two categories built on the tools of internal/tools (spec 3.1, rules in packs/caches.toml):
// caches (global package-manager caches, cleaned only by the tool's own command and only in the low-disk tier,
// largest first, plus report-only locations and old Gradle daemon logs) and mcp-runtime (the weekly prunes).
package caches

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/tools"
)

const weekly = 7 * 24 * time.Hour

// reportOnly are the locations nothing may clean; each resolves to the directory to show, or "" when absent.
var reportOnly = map[string]func(env *core.Env) string{
	"npm-npx":        npxDir,
	"gradle-caches":  underHome(".gradle", "caches"),
	"maven-repo":     underHome(".m2", "repository"),
	"cargo-registry": underHome(".cargo", "registry"),
	"cargo-git":      underHome(".cargo", "git"),
	"deno-cache":     denoDir,
}

const daemonLogs = "gradle-daemon-logs"

func init() {
	for id := range reportOnly {
		core.RegisterLocation(id)
	}
	core.RegisterLocation(daemonLogs)
	core.Register(&category{name: "caches"})
	core.Register(&category{name: "mcp-runtime"})
	engine.SetLowDisk(func(env *core.Env, _ *engine.Summary) []core.Candidate { return lowDiskCandidates(env) })
}

func underHome(rel ...string) func(*core.Env) string {
	return func(env *core.Env) string {
		return existingDir(filepath.Join(append([]string{env.Home}, rel...)...))
	}
}

func existingDir(p string) string {
	if fi, err := os.Lstat(p); err != nil || !fi.IsDir() {
		return ""
	}
	return p
}

// npxDir is npm's _npx folder inside the cache dir npm reports.
func npxDir(env *core.Env) string {
	dir, ok := toolDir(env, "npm")
	if !ok {
		return ""
	}
	return existingDir(filepath.Join(dir, "_npx"))
}

func denoDir(env *core.Env) string {
	if _, err := env.Run.LookPath("deno"); err != nil {
		return ""
	}
	out, err := tools.Query(env, "deno", "info", "--json")
	if err != nil {
		return ""
	}
	var info struct {
		DenoDir string `json:"denoDir"`
	}
	if json.Unmarshal([]byte(out), &info) != nil || !filepath.IsAbs(info.DenoDir) {
		return ""
	}
	return existingDir(filepath.Clean(info.DenoDir))
}

// toolDir asks a tool for its cache dir; a tool that is missing, silent or slow, or a dir that does not exist,
// is skipped.
// The answer is remembered for the run (the same env value and time), so one run asks each tool once.
func toolDir(env *core.Env, id string) (string, bool) {
	if dir, ok, seen := memo.get(env, id); seen {
		return dir, ok
	}
	dir, ok := queryToolDir(env, id)
	memo.put(env, id, dir, ok)
	return dir, ok
}

func queryToolDir(env *core.Env, id string) (string, bool) {
	t, ok := tools.Get(id)
	if !ok {
		return "", false
	}
	if _, err := env.Run.LookPath(id); err != nil {
		return "", false
	}
	dir, err := t.Dir(env)
	if err != nil || existingDir(dir) == "" {
		return "", false
	}
	return dir, true
}

type dirAnswer struct {
	dir string
	ok  bool
}

// dirMemo holds the tool dir answers of the latest run only.
type dirMemo struct {
	mu  sync.Mutex
	env *core.Env
	now time.Time
	m   map[string]dirAnswer
}

var memo dirMemo

func (d *dirMemo) get(env *core.Env, id string) (dir string, ok, seen bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.env != env || !d.now.Equal(env.Now) {
		return "", false, false
	}
	a, seen := d.m[id]
	return a.dir, a.ok, seen
}

func (d *dirMemo) put(env *core.Env, id, dir string, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.env != env || !d.now.Equal(env.Now) {
		d.env, d.now, d.m = env, env.Now, map[string]dirAnswer{}
	}
	d.m[id] = dirAnswer{dir, ok}
}

// toolCandidate is the official-command candidate of one cache rule.
func toolCandidate(env *core.Env, r core.Rule) (core.Candidate, bool) {
	t, ok := tools.Get(r.Tool)
	argv, okAction := t.Actions[r.Action]
	if !ok || !okAction {
		return core.Candidate{}, false
	}
	dir, ok := toolDir(env, r.Tool)
	if !ok {
		return core.Candidate{}, false
	}
	cmd := append([]string{t.ID}, argv...)
	return core.Candidate{
		Category: r.Category, RuleKey: r.Key(), Path: dir, Base: filepath.Dir(dir), Removal: core.ToolCommand,
		Command: cmd, Reason: "cache of " + t.ID + ", cleaned by its own command: " + strings.Join(cmd, " "),
	}, true
}

func rulesOf(env *core.Env, category string) []core.Rule {
	var out []core.Rule
	for _, r := range env.Rules {
		if r.Category == category {
			out = append(out, r)
		}
	}
	return out
}

// lowDiskCandidates are the enabled caches candidates, largest first (unknown sizes last).
func lowDiskCandidates(env *core.Env) []core.Candidate {
	if env.Settings == nil || env.Run == nil || !env.Settings.CategoryEnabled("caches") {
		return nil
	}
	type sized struct {
		c    core.Candidate
		size int64
	}
	var list []sized
	for _, r := range rulesOf(env, "caches") {
		if r.Kind != core.KindCache || r.Bucket == core.Never || !env.Settings.RuleEnabled(r.Key()) {
			continue
		}
		if env.Ctx != nil && env.Ctx.Err() != nil {
			break
		}
		if c, ok := toolCandidate(env, r); ok {
			ctx := env.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			list = append(list, sized{c, engine.SizeContext(ctx, c.Path)})
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i].size, list[j].size
		if (a < 0) != (b < 0) {
			return b < 0
		}
		return a > b
	})
	out := make([]core.Candidate, len(list))
	for i := range list {
		out[i] = list[i].c
		out[i].Bytes = list[i].size
	}
	return out
}

type category struct{ name string }

func (c *category) Name() string      { return c.name }
func (*category) Bucket() core.Bucket { return core.Optional }
func (c *category) stampPath() (string, error) {
	p, err := config.ResolvePaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(p.State, "tools", c.name+".last"), nil
}

// lastRun is the time the weekly prunes last went ahead; zero when never, or when the stamp lies in the future
// (the clock went back), which counts as never run.
func (c *category) lastRun(now time.Time) time.Time {
	p, err := c.stampPath()
	if err != nil {
		return time.Time{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if t.After(now) {
		return time.Time{}
	}
	return t
}

// recordRun writes the stamp atomically: a 0600 temp file in the same directory, then a rename.
func (c *category) recordRun(now time.Time) error {
	p, err := c.stampPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// A fixed temp name next to the stamp: a failed write leaves at most this one file, which the next run
	// overwrites (this package may not delete files).
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(now.UTC().Format(time.RFC3339) + "\n")
	if err := errors.Join(werr, f.Close()); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// AfterRemove records the weekly prune once the engine really removed a candidate of mcp-runtime.
func (c *category) AfterRemove(env *core.Env, cand core.Candidate) {
	if c.name != "mcp-runtime" || cand.Removal != core.ToolCommand {
		return
	}
	if err := c.recordRun(env.Now); err != nil && env.Log != nil {
		env.Log.Event("stamp-failed", map[string]any{"category": c.name, "error": err.Error()})
	}
}

// Scan of caches proposes only report-only locations and old Gradle daemon logs: the tool commands run in the
// low-disk tier. Scan of mcp-runtime proposes the weekly prunes, at most once per 7 days.
func (c *category) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	var cands []core.Candidate
	if c.name == "mcp-runtime" {
		if last := c.lastRun(env.Now); !last.IsZero() && env.Now.Sub(last) < weekly {
			return nil, nil, nil
		}
		for _, r := range rulesOf(env, c.name) {
			if r.Kind != core.KindCache || r.Bucket == core.Never || !env.Settings.RuleEnabled(r.Key()) {
				continue
			}
			if cand, ok := toolCandidate(env, r); ok {
				cands = append(cands, cand)
			}
		}
		return cands, nil, nil
	}
	for _, r := range rulesOf(env, c.name) {
		if r.Kind != core.KindPath {
			continue
		}
		if env.Ctx != nil && env.Ctx.Err() != nil {
			break
		}
		if resolve, ok := reportOnly[r.Location]; ok {
			if dir := resolve(env); dir != "" {
				reason := "no official command cleans this cache; shown, never removed"
				if r.Location == "npm-npx" {
					reason = "npm has no command for _npx alone, so it is shown and never removed"
				}
				cands = append(cands, core.Candidate{Category: c.name, RuleKey: r.Key(), Path: dir,
					Base: filepath.Dir(dir), Removal: core.ReportOnly, Reason: reason})
			}
		} else if r.Location == daemonLogs {
			cands = append(cands, daemonLogCandidates(env, r)...)
		}
	}
	return cands, nil, nil
}

func daemonLogCandidates(env *core.Env, r core.Rule) []core.Candidate {
	daemon := filepath.Join(env.Home, ".gradle", "daemon")
	versions, err := os.ReadDir(daemon)
	if err != nil {
		return nil
	}
	var out []core.Candidate
	for _, v := range versions {
		if !v.IsDir() { // a symlink or a file is not a daemon version folder
			continue
		}
		dir := filepath.Join(daemon, v.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range files {
			if !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			fi, err := os.Lstat(p)
			if err != nil || !fi.Mode().IsRegular() || env.Now.Sub(fi.ModTime()) < r.MinAge {
				continue
			}
			out = append(out, core.Candidate{Category: r.Category, RuleKey: r.Key(), Path: p, Base: dir,
				Removal: core.RenameDelete, Reason: "Gradle daemon log untouched for " + r.MinAge.String()})
		}
	}
	return out
}

// Proof re-checks that the candidate is exactly what its rule says: the tool command of the rule's action, or a
// regular .log file directly below ~/.gradle/daemon/<version>. It has no side effect.
func (c *category) Proof(env *core.Env, cand core.Candidate) error {
	var rule *core.Rule
	for _, r := range rulesOf(env, c.name) {
		if r.Key() == cand.RuleKey {
			r := r
			rule = &r
			break
		}
	}
	if rule == nil || cand.Category != c.name {
		return core.Keep("error", "no "+c.name+" rule "+cand.RuleKey)
	}
	switch cand.Removal {
	case core.ToolCommand:
		t, ok := tools.Get(rule.Tool)
		argv, okAction := t.Actions[rule.Action]
		if rule.Kind != core.KindCache || !ok || !okAction || !slices.Equal(cand.Command, append([]string{t.ID}, argv...)) {
			return core.Keep("error", "command is not the official action of "+cand.RuleKey)
		}
		return nil
	case core.RenameDelete:
		daemon := filepath.Join(env.Home, ".gradle", "daemon")
		if rule.Location != daemonLogs || !strings.HasSuffix(cand.Path, ".log") ||
			filepath.Dir(cand.Path) != cand.Base || filepath.Dir(cand.Base) != daemon {
			return core.Keep("no-marker", "not a Gradle daemon log: "+cand.Path)
		}
		fi, err := os.Lstat(cand.Path)
		if err != nil {
			return core.Keep("error", err.Error())
		}
		if !fi.Mode().IsRegular() {
			return core.Keep("place", "not a regular file: "+cand.Path)
		}
		return nil
	}
	return core.Keep("error", fmt.Sprintf("%s never removes a %s candidate", c.name, cand.Removal))
}
