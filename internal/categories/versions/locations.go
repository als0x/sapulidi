package versions

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/tools"
	"github.com/als0x/sapulidi/internal/tools/strategies"
)

// locations maps a location id of a pack rule to the code that finds its versions. A location that is not
// installed answers false. Go toolchains are not here on purpose: they live inside GOMODCACHE, which is never
// cleaned automatically.
var locations = map[string]func(env *core.Env) (found, bool){
	"nvm-versions":        nvmVersions,
	"fnm-versions":        fnmVersions,
	"volta-tools":         voltaTools,
	"pyenv-versions":      pyenvVersions,
	"rustup-toolchains":   rustupToolchains,
	"playwright-browsers": playwrightBrowsers,
}

func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return lines
}

func envOr(name string, def func() string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def()
}

// isVersionName filters what a versions directory may offer: a version has a digit in its name; names that
// start with ".", aliases (default, system, current, lts, latest), lock files and partial downloads are
// never versions.
func isVersionName(n string) bool {
	if n == "" || n[0] == '.' || !strings.ContainsAny(n, "0123456789") {
		return false
	}
	low := strings.ToLower(n)
	for _, s := range []string{".lock", ".tmp", ".part", ".partial", ".download", ".downloading", ".incomplete", "-partial", "~"} {
		if strings.HasSuffix(low, s) || strings.Contains(low, s+".") {
			return false
		}
	}
	return !strings.HasPrefix(low, "tmp") && !strings.Contains(low, "partial")
}

// scan lists the version directories in base; false when base is missing or unreadable.
func scan(base string) (found, bool) {
	names, err := listDirs(base)
	if err != nil {
		return found{}, false
	}
	f := found{base: base}
	for _, n := range names {
		if isVersionName(n) {
			f.versions = append(f.versions, strategies.Version{Name: n, Path: filepath.Join(base, n)})
		}
	}
	return f, true
}

func has(f found, name string) bool {
	for _, v := range f.versions {
		if v.Name == name {
			return true
		}
	}
	return false
}

// pathNode returns the version directory the `node` on PATH lives in, when it lives below base.
func pathNode(env *core.Env, base string) string {
	p, err := env.Run.LookPath("node")
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return strings.Split(rel, string(filepath.Separator))[0]
}

func newest(f found, match func(string) bool) (string, bool) {
	best := ""
	for _, v := range f.versions {
		if match(v.Name) && (best == "" || strategies.Compare(v.Name, best) > 0) {
			best = v.Name
		}
	}
	return best, best != ""
}

// resolveActive protects what an active name reaches through symlinks: a pyenv virtualenv "myenv" that links
// to 3.12.1/envs/myenv makes 3.12.1 active. An active name that exists but does not resolve is a doubt.
func (f *found) resolveActive() {
	if !f.activeKnown {
		return
	}
	realBase, err := filepath.EvalSymlinks(f.base)
	if err != nil {
		f.activeKnown = false
		return
	}
	for _, name := range slices.Clone(f.active) {
		p := filepath.Join(f.base, name)
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			continue // not installed here (system)
		} else if err != nil {
			f.activeKnown = false
			return
		}
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			f.activeKnown = false
			return
		}
		rel, err := filepath.Rel(realBase, real)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		f.active = append(f.active, strings.Split(rel, string(filepath.Separator))[0])
	}
}

// applyPins adds the versions named by the pin files of every root (and extra pins) to f.pinned. kind is
// "node" (.nvmrc, .node-version, .tool-versions nodejs) or "python" (.python-version, .tool-versions python).
// A pin file that cannot be read, or a node pin that names no version (lts/*, node), is a doubt.
func (f *found) applyPins(env *core.Env, kind string, extra []string) {
	files, keys := []string{".python-version", ".tool-versions"}, []string{"python"}
	if kind == "node" {
		files, keys = []string{".nvmrc", ".node-version", ".tool-versions"}, []string{"nodejs", "node"}
	}
	var pins []string
	for _, file := range files {
		contents, err := readRootFile(env, file)
		if err != nil {
			f.activeKnown = false
			return
		}
		for _, c := range contents {
			for _, line := range splitLines(c) {
				line, _, _ = strings.Cut(line, "#")
				fields := strings.Fields(line)
				if len(fields) == 0 {
					continue
				}
				if file == ".tool-versions" {
					if !slices.Contains(keys, fields[0]) {
						continue
					}
					fields = fields[1:]
				}
				pins = append(pins, fields...)
			}
		}
	}
	pins = append(pins, extra...)
	trim := func(s string) string {
		if kind == "node" {
			return strings.TrimPrefix(s, "v")
		}
		return s
	}
	for _, p := range pins {
		p = trim(p)
		if p == "" || p == "system" {
			continue
		}
		if kind == "node" && (p[0] < '0' || p[0] > '9') {
			f.activeKnown = false
			return
		}
		for _, v := range f.versions {
			if pinMatches(trim(v.Name), p) && !slices.Contains(f.pinned, v.Name) {
				f.pinned = append(f.pinned, v.Name)
			}
		}
	}
}

// ---- nvm ----

func nvmVersions(env *core.Env) (found, bool) {
	dir := envOr("NVM_DIR", func() string { return filepath.Join(env.Home, ".nvm") })
	f, ok := scan(filepath.Join(dir, "versions", "node"))
	if !ok {
		return f, false
	}
	if name, ok := nvmDefault(dir, f); ok {
		f.activeKnown = true
		if name != "" {
			f.active = append(f.active, name)
		}
		if n := pathNode(env, f.base); n != "" {
			f.active = append(f.active, n)
		}
		f.resolveActive()
		f.applyPins(env, "node", nil)
	}
	return f, true
}

// nvmDefault resolves $NVM_DIR/alias/default through other aliases to an installed version ("" with ok for
// "system"). It is not ok for anything it cannot resolve.
func nvmDefault(dir string, f found) (string, bool) {
	val := "default"
	for i := range 6 {
		if val == "" || strings.Contains(val, "..") || filepath.IsAbs(val) {
			return "", false
		}
		b, err := os.ReadFile(filepath.Join(dir, "alias", filepath.FromSlash(val)))
		if err == nil {
			val = strings.TrimSpace(string(b))
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) || i == 0 {
			return "", false // no default alias, or unreadable
		}
		break
	}
	switch val {
	case "system":
		return "", true
	case "node", "stable":
		return newest(f, func(string) bool { return true })
	}
	v := strings.TrimPrefix(val, "v")
	return newest(f, func(n string) bool {
		n = strings.TrimPrefix(n, "v")
		return n == v || strings.HasPrefix(n, v+".")
	})
}

// ---- fnm ----

func fnmVersions(env *core.Env) (found, bool) {
	var dirs []string
	if d := os.Getenv("FNM_DIR"); d != "" {
		dirs = []string{d}
	} else {
		dirs = []string{filepath.Join(env.Home, ".local", "share", "fnm"), filepath.Join(env.Home, ".fnm")}
	}
	for _, dir := range dirs {
		f, ok := scan(filepath.Join(dir, "node-versions"))
		if !ok {
			continue
		}
		if name, ok := fnmDefault(dir); ok && has(f, name) {
			f.activeKnown = true
			f.active = append(f.active, name)
			if n := pathNode(env, f.base); n != "" {
				f.active = append(f.active, n)
			}
			f.resolveActive()
			f.applyPins(env, "node", nil)
		}
		return f, true
	}
	return found{}, false
}

// fnmDefault reads the symlink $FNM_DIR/aliases/default, which points at node-versions/<name>/installation.
func fnmDefault(dir string) (string, bool) {
	link := filepath.Join(dir, "aliases", "default")
	target, err := os.Readlink(link)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	parts := strings.Split(filepath.Clean(target), string(filepath.Separator))
	for i, p := range parts {
		if p == "node-versions" && i+1 < len(parts) {
			return parts[i+1], true
		}
	}
	return "", false
}

// ---- volta ----

func voltaTools(env *core.Env) (found, bool) {
	home := envOr("VOLTA_HOME", func() string { return filepath.Join(env.Home, ".volta") })
	f, ok := scan(filepath.Join(home, "tools", "image", "node"))
	if !ok {
		return f, false
	}
	b, err := os.ReadFile(filepath.Join(home, "tools", "user", "platform.json"))
	if err != nil {
		return f, true
	}
	var p struct {
		Node *struct {
			Runtime string `json:"runtime"`
		} `json:"node"`
	}
	if json.Unmarshal(b, &p) == nil && p.Node != nil && has(f, p.Node.Runtime) {
		f.activeKnown, f.active = true, []string{p.Node.Runtime}
		if n := pathNode(env, f.base); n != "" {
			f.active = append(f.active, n)
		}
		f.resolveActive()
		f.applyPins(env, "node", nil)
	}
	return f, true
}

// ---- pyenv ----

func pyenvVersions(env *core.Env) (found, bool) {
	root := envOr("PYENV_ROOT", func() string { return filepath.Join(env.Home, ".pyenv") })
	f, ok := scan(filepath.Join(root, "versions"))
	if !ok {
		return f, false
	}
	known := false
	if _, err := env.Run.LookPath("pyenv"); err == nil {
		line, err := query(env, "pyenv", "version-name")
		if err != nil {
			return f, true // the tool is there but does not answer: unknown
		}
		f.active = append(f.active, strings.Split(line, ":")...)
		known = true
	}
	switch b, err := os.ReadFile(filepath.Join(root, "version")); {
	case err == nil:
		for _, l := range splitLines(string(b)) {
			f.active = append(f.active, strings.Split(l, ":")...)
		}
		known = true
	case !errors.Is(err, fs.ErrNotExist):
		known = false
	}
	f.activeKnown = known
	f.resolveActive()
	f.applyPins(env, "python", strings.Split(os.Getenv("PYENV_VERSION"), ":"))
	return f, true
}

// ---- rustup ----

func rustupToolchains(env *core.Env) (found, bool) {
	home := envOr("RUSTUP_HOME", func() string { return filepath.Join(env.Home, ".rustup") })
	f, ok := scan(filepath.Join(home, "toolchains"))
	if !ok {
		return f, false
	}
	f.uninstall = func(name string) []string {
		if !safeName.MatchString(name) {
			return nil
		}
		return []string{"rustup", "toolchain", "uninstall", name}
	}
	line, err := query(env, "rustup", "default")
	if err != nil {
		return f, true
	}
	def := strings.Fields(line)
	if len(def) == 0 || !has(f, def[0]) {
		return f, true
	}
	pins, ok := rustPins(env)
	if !ok {
		return f, true
	}
	overrides, ok := rustOverrides(env)
	if !ok {
		return f, true
	}
	pins = append(pins, overrides...)
	if t := os.Getenv("RUSTUP_TOOLCHAIN"); t != "" {
		pins = append(pins, t)
	}
	f.activeKnown, f.active = true, []string{def[0]}
	for _, v := range f.versions {
		for _, p := range pins {
			if pinMatches(v.Name, p) {
				f.pinned = append(f.pinned, v.Name)
			}
		}
	}
	return f, true
}

// rustOverrides lists the toolchains of `rustup override list` (lines "path<TAB>toolchain"; "no overrides"
// means none). A failing command or a line of another shape is a doubt.
func rustOverrides(env *core.Env) ([]string, bool) {
	out, err := tools.Query(env, "rustup", "override", "list")
	if err != nil {
		return nil, false
	}
	var tcs []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l == "no overrides" {
			continue
		}
		i := strings.LastIndex(l, "\t")
		if i < 0 || strings.TrimSpace(l[i+1:]) == "" {
			return nil, false
		}
		tcs = append(tcs, strings.TrimSpace(l[i+1:]))
	}
	return tcs, true
}

// rustPins lists the toolchain names written in the rust-toolchain files of the roots. A file that cannot be
// read, or a TOML file that does not parse but has a channel line, is a doubt (false).
func rustPins(env *core.Env) ([]string, bool) {
	var pins []string
	for _, name := range []string{"rust-toolchain", "rust-toolchain.toml"} {
		files, err := readRootFile(env, name)
		if err != nil {
			return nil, false
		}
		for _, content := range files {
			var t struct {
				Toolchain struct{ Channel string }
			}
			err := toml.Unmarshal([]byte(content), &t)
			if err == nil && t.Toolchain.Channel != "" {
				pins = append(pins, t.Toolchain.Channel)
				continue
			}
			if err != nil && strings.Contains(content, "channel") {
				return nil, false
			}
			for _, l := range splitLines(content) {
				if l != "" && !strings.HasPrefix(l, "#") && !strings.Contains(l, "=") && !strings.HasPrefix(l, "[") {
					pins = append(pins, l)
					break
				}
			}
		}
	}
	return pins, true
}

// ---- playwright ----

func playwrightBrowsers(env *core.Env) (found, bool) {
	base := filepath.Join(env.Home, ".cache", "ms-playwright")
	if env.Platform != nil && env.Platform.OS() == "darwin" {
		base = filepath.Join(env.Home, "Library", "Caches", "ms-playwright")
	}
	f, ok := scan(base)
	f.activeKnown = true
	return f, ok
}
