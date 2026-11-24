package guard

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

// NestedBudget bounds the whole proof about the repositories inside one project artefact.
var NestedBudget = 20 * time.Second

// MaxNested is the most nested repositories a project artefact may hold and still be proven.
const MaxNested = 16

type nestedRepo struct {
	path string // absolute
	kind string // "git" (a .git directory), "gitfile", "bare" or "otherfs" (a directory on another filesystem)
}

// statDev is the device of fi; a seam so a test can fake a second filesystem.
var statDev = func(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // Dev is int32 on darwin
}

// maxRead caps what the proof reads from a repository's config and from git's output.
const maxRead = 1 << 20

// nestedRepos finds the repositories inside dir (dir itself included): a .git directory or file, or a bare
// repository. It follows no symlink, stays on dir's filesystem and does not look inside .git directories. A walk that
// finds more than MaxNested stops and reports tooMany.
func nestedRepos(ctx context.Context, dir string) (repos []nestedRepo, tooMany bool, err error) {
	if abs, e := filepath.Abs(dir); e == nil {
		dir = abs
	}
	var rootDev uint64
	var haveDev bool
	if fi, e := os.Lstat(dir); e == nil {
		rootDev, haveDev = statDev(fi)
	}
	add := func(r nestedRepo) bool {
		if len(repos) == MaxNested {
			tooMany = true
			return false
		}
		repos = append(repos, r)
		return true
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if gn := gitName(d.Name()); gn != 0 {
			r := nestedRepo{path: filepath.Dir(p), kind: "git"}
			if !d.IsDir() {
				r.kind = "gitfile"
			}
			if gn == 2 {
				r.kind = "gitname"
			}
			if !add(r) {
				return fs.SkipAll
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if haveDev && p != dir {
			if info, e := d.Info(); e == nil {
				if dev, ok := statDev(info); ok && dev != rootDev {
					if !add(nestedRepo{path: p, kind: "otherfs"}) {
						return fs.SkipAll
					}
					return fs.SkipDir
				}
			}
		}
		if isBareRepo(p) {
			if !add(nestedRepo{path: p, kind: "bare"}) {
				return fs.SkipAll
			}
			return fs.SkipDir
		}
		return nil
	})
	return repos, tooMany, err
}

// nestedProof is the proof that every repository inside the project artefact dir is safe to discard: a clean work
// tree, no stash, a remote, and no commit (on a branch or on HEAD) that no remote branch holds. Anything else, a git
// error or the time budget included, keeps the artefact as nested-git, naming the first failing repository.
func nestedProof(run core.Runner, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), NestedBudget)
	defer cancel()
	repos, tooMany, err := nestedRepos(ctx, dir)
	if err != nil {
		return core.Keep("nested-git", "cannot search "+filepath.Base(dir)+" for repositories: "+err.Error())
	}
	if tooMany {
		return core.Keep("nested-git", "more than 16 nested repositories")
	}
	for _, r := range repos {
		if reason := nestedUnsafe(ctx, run, r); reason != "" {
			rel, e := filepath.Rel(dir, r.path)
			if e != nil {
				rel = r.path
			}
			return core.Keep("nested-git", rel+": "+reason)
		}
	}
	return nil
}

type nestedConfig struct {
	remotes map[string][]string // remote name -> url and pushurl values
}

// allowedConfig is every section and key a plain clone's .git/config may hold. Everything else (a filter driver,
// include, core.attributesFile, core.worktree, core.fsmonitor, core.hooksPath, core.sshCommand, a key that names a
// command, extensions) keeps the repository: the config is read as a file, never through git.
var allowedConfig = map[string][]string{
	"core":   {"repositoryformatversion", "filemode", "bare", "logallrefupdates", "ignorecase", "precomposeunicode", "symlinks"},
	"remote": {"url", "pushurl", "fetch", "tagopt", "prune"},
	"branch": {"remote", "merge", "rebase", "description", "vscode-merge-base", "pushremote"},
	"user":   {"name", "email"},
}

func configName(s string, allowDot bool) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || (allowDot && c == '.')
		if !ok {
			return false
		}
	}
	return true
}

// scanConfig parses the config file at path with an allow-list. reason is "" for a missing file or a config made only
// of allowed settings, and "config" (or "bare") otherwise, including whenever the text cannot be parsed with confidence.
func scanConfig(path string) (cfg nestedConfig, reason string) {
	cfg.remotes = map[string][]string{}
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return cfg, ""
	}
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxRead {
		return cfg, "config"
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > maxRead {
		return cfg, "config"
	}
	// git's line rules (a backslash continues a value but not a comment, a lone CR) are not emulated: a plain clone
	// config has no backslash, no NUL and no CR outside a CRLF line end, so anything else keeps the repository.
	text := string(raw)
	if strings.ContainsAny(text, "\\\x00") || strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\r") {
		return cfg, "config"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	section, sub := "", ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || l[0] == '#' || l[0] == ';' {
			continue
		}
		if l[0] == '[' {
			rest, tail := l[1:], ""
			if j := strings.IndexByte(rest, '"'); j >= 0 {
				k := strings.IndexByte(rest[j+1:], '"')
				if k < 0 {
					return cfg, "config"
				}
				k += j + 1
				section, sub = strings.TrimSpace(rest[:j]), rest[j+1:k]
				tail = strings.TrimSpace(rest[k+1:])
				if !strings.HasPrefix(tail, "]") {
					return cfg, "config"
				}
				tail = strings.TrimSpace(tail[1:])
			} else {
				i := strings.IndexByte(rest, ']')
				if i < 0 {
					return cfg, "config"
				}
				section, sub, tail = rest[:i], "", strings.TrimSpace(rest[i+1:])
				if d := strings.IndexByte(section, '.'); d >= 0 {
					section, sub = section[:d], section[d+1:]
				}
			}
			if tail != "" && tail[0] != '#' && tail[0] != ';' {
				return cfg, "config"
			}
			section = strings.ToLower(section)
			if !configName(section, false) || (sub == "") != (section != "remote" && section != "branch") {
				return cfg, "config"
			}
			if _, ok := allowedConfig[section]; !ok {
				return cfg, "config"
			}
			continue
		}
		key, val, _ := strings.Cut(l, "=")
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
		if section == "" || !configName(key, false) || key[0] < 'a' || key[0] > 'z' {
			return cfg, "config"
		}
		allowed := false
		for _, k := range allowedConfig[section] {
			allowed = allowed || k == key
		}
		if !allowed {
			return cfg, "config"
		}
		switch {
		case section == "core" && key == "bare":
			if v := strings.ToLower(val); v != "false" && v != "no" && v != "off" && v != "0" {
				return cfg, "bare"
			}
		case section == "remote" && (key == "url" || key == "pushurl"):
			if strings.ContainsAny(val, "\"\\#;") || !remoteName(sub) {
				return cfg, "config"
			}
			cfg.remotes[sub] = append(cfg.remotes[sub], val)
		case section == "remote" && key != "fetch" && key != "prune" && key != "tagopt":
			return cfg, "config"
		}
	}
	for name, urls := range cfg.remotes {
		if len(urls) == 0 {
			delete(cfg.remotes, name)
		}
	}
	return cfg, ""
}

// networkURL reports a remote url that names another machine: http(s), ssh, git or host:path. A local path, a relative
// path, file: and ext:: make "pushed to the remote" prove nothing.
func networkURL(u string) bool {
	u = strings.TrimSpace(u)
	lower := strings.ToLower(u)
	if u == "" || strings.HasPrefix(lower, "file:") || strings.Contains(u, "::") {
		return false
	}
	if i := strings.Index(u, "://"); i > 0 {
		switch lower[:i] {
		case "http", "https", "ssh", "git":
			auth, _, _ := strings.Cut(u[i+3:], "/")
			if at := strings.LastIndexByte(auth, '@'); at >= 0 {
				auth = auth[at+1:]
			}
			if strings.HasPrefix(auth, "[") {
				auth, _, _ = strings.Cut(strings.TrimPrefix(auth, "["), "]")
			} else {
				auth, _, _ = strings.Cut(auth, ":")
			}
			return !loopbackHost(auth)
		}
		return false
	}
	c := strings.IndexByte(u, ':')
	if c <= 1 || strings.Contains(u[:c], "/") {
		return false
	}
	host := u[:c]
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	return !loopbackHost(host)
}

// loopbackHost is an empty, local or loopback host: a remote there is this machine, not a place work was pushed to.
func loopbackHost(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	return h == "" || h == "localhost" || h == "0.0.0.0" || h == "::1" || strings.HasPrefix(h, "127.")
}

// remoteName accepts the plain names a clone gives a remote; the name goes into a ref glob.
func remoteName(s string) bool {
	if s == "" || s[0] == '-' || s[0] == '.' {
		return false
	}
	return configName(strings.ReplaceAll(strings.ReplaceAll(s, "_", "-"), "/", "-"), true)
}

// hfsIgnorable are the code points HFS+ and APFS ignore inside a name, so ".g<U+200C>it" is ".git" to git there.
func hfsIgnorable(r rune) bool {
	return r >= 0x200c && r <= 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x206a && r <= 0x206f || r == 0xfeff
}

// gitName classifies a directory entry name: 0 not .git, 1 .git (any case), 2 .git spelled with ignorable characters.
func gitName(name string) int {
	stripped := strings.Map(func(r rune) rune {
		if hfsIgnorable(r) {
			return -1
		}
		return r
	}, name)
	switch {
	case !strings.EqualFold(stripped, ".git"):
		return 0
	case stripped != name:
		return 2
	}
	return 1
}

func present(path string) bool {
	_, err := os.Lstat(path)
	return !os.IsNotExist(err)
}

func nonEmptyDir(path string) bool {
	es, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return false
	}
	return err != nil || len(es) > 0
}

// scanGitDir keeps a repository that borrows objects, rewrites history or has linked worktrees: the work could live
// outside the artefact.
func scanGitDir(gitDir string) string {
	if present(filepath.Join(gitDir, "objects", "info", "alternates")) ||
		present(filepath.Join(gitDir, "objects", "info", "http-alternates")) {
		return "alternates"
	}
	// These send git to a config, objects or refs somewhere other than what was scanned.
	for _, n := range []string{"commondir", "gitdir", "config.worktree"} {
		if present(filepath.Join(gitDir, n)) {
			return "redirect"
		}
	}
	if present(filepath.Join(gitDir, "info", "grafts")) || nonEmptyDir(filepath.Join(gitDir, "refs", "replace")) {
		return "replace"
	}
	if raw, err := os.ReadFile(filepath.Join(gitDir, "packed-refs")); err == nil {
		if len(raw) > maxRead {
			return "error"
		}
		if strings.Contains(string(raw), "refs/replace/") {
			return "replace"
		}
	} else if !os.IsNotExist(err) {
		return "error"
	}
	if nonEmptyDir(filepath.Join(gitDir, "worktrees")) {
		return "worktrees"
	}
	return ""
}

func samePath(a, b string) bool {
	ra, e1 := filepath.EvalSymlinks(strings.TrimSpace(a))
	rb, e2 := filepath.EvalSymlinks(b)
	return e1 == nil && e2 == nil && filepath.Clean(ra) == filepath.Clean(rb)
}

// nestedUnsafe returns why r cannot be discarded, or "" when it can.
func nestedUnsafe(ctx context.Context, run core.Runner, r nestedRepo) string {
	switch r.kind {
	case "gitfile":
		return "gitfile"
	case "bare":
		return "bare"
	case "otherfs":
		return "other-filesystem"
	case "gitname":
		return "gitname"
	}
	// Files first: nothing below starts git until the repository's own config, alternates, replace refs and linked
	// worktrees are known to be plain.
	gitDir := filepath.Join(r.path, ".git")
	cfg, reason := scanConfig(filepath.Join(gitDir, "config"))
	if reason != "" {
		return reason
	}
	if reason := scanGitDir(gitDir); reason != "" {
		return reason
	}
	g := func(args ...string) (string, bool) {
		if ctx.Err() != nil {
			return "", false
		}
		out, err := gitOutCtx(ctx, run, r.path, args...)
		return out, err == nil && len(out) <= maxRead
	}
	// The repository git sees must be this one: an empty or broken .git directory makes git walk up to the
	// project's repository and judge that instead.
	out, ok := g("rev-parse", "--absolute-git-dir", "--show-toplevel")
	if !ok {
		return "error"
	}
	if ids := strings.Split(strings.TrimSpace(out), "\n"); len(ids) != 2 ||
		!samePath(ids[0], gitDir) || !samePath(ids[1], r.path) {
		return "identity"
	}
	if len(cfg.remotes) == 0 {
		return "no-remote"
	}
	for _, urls := range cfg.remotes {
		for _, u := range urls {
			if !networkURL(u) {
				return "local-remote"
			}
		}
	}
	out, ok = g("--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if !ok {
		return "error"
	}
	if strings.TrimSpace(out) != "" {
		for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
			if !strings.HasPrefix(line, "??") {
				return "dirty"
			}
		}
		return "untracked"
	}
	if out, ok = g("stash", "list"); !ok {
		return "error"
	} else if strings.TrimSpace(out) != "" {
		return "stash"
	}
	// --all: every local ref, tags, notes and stash included, and HEAD.
	// Only the refs of the validated remotes count, not any hand-made refs/remotes/<x>/ branch.
	args := []string{"--no-replace-objects", "rev-list", "--all", "--max-count=1", "--not"}
	names := make([]string, 0, len(cfg.remotes))
	for name := range cfg.remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		args = append(args, "--glob=refs/remotes/"+name+"/*")
	}
	if out, ok = g(args...); !ok {
		return "error"
	} else if strings.TrimSpace(out) != "" {
		return "unpushed"
	}
	return ""
}
