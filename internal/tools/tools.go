// Package tools describes the package managers and runtimes whose caches sapulidi cleans, and only through the
// tool's own command (spec 5.1.1): how to ask the tool where its cache is, and which actions it offers.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

// Tool is one cache-owning tool. Actions maps an action id to the argv that follows the tool id.
type Tool struct {
	ID      string
	Dir     func(env *core.Env) (string, error)
	Actions map[string][]string
}

// QueryTimeout bounds every cache-dir query.
var QueryTimeout = 10 * time.Second

var registry = map[string]Tool{}

// Get returns the tool with the given id.
func Get(id string) (Tool, bool) {
	t, ok := registry[id]
	return t, ok
}

// IDs lists every tool id, sorted.
func IDs() []string {
	var out []string
	for id := range registry {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Query runs argv (a read-only question to a tool) with the query timeout and returns its stdout.
func Query(env *core.Env, argv ...string) (string, error) {
	parent := env.Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, QueryTimeout)
	defer cancel()
	out, err := env.Run.Run(ctx, env.Home, argv...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// firstLine runs argv and returns its first non-empty output line, which must be an absolute path.
func firstLine(env *core.Env, argv ...string) (string, error) {
	out, err := Query(env, argv...)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return absolute(argv[0], line)
		}
	}
	return "", fmt.Errorf("%s: empty answer", argv[0])
}

func absolute(tool, p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s: %q is not an absolute path", tool, p)
	}
	return filepath.Clean(p), nil
}

func query(argv ...string) func(*core.Env) (string, error) {
	return func(env *core.Env) (string, error) { return firstLine(env, argv...) }
}

// dotnetDir picks the global-packages line of `dotnet nuget locals all --list`.
func dotnetDir(env *core.Env) (string, error) {
	out, err := Query(env, "dotnet", "nuget", "locals", "all", "--list")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "global-packages:"); ok {
			return absolute("dotnet", strings.TrimSpace(p))
		}
	}
	return "", errors.New("dotnet: no global-packages line")
}

// condaDir returns the first of pkgs_dirs in `conda info --json`.
func condaDir(env *core.Env) (string, error) {
	out, err := Query(env, "conda", "info", "--json")
	if err != nil {
		return "", err
	}
	var info struct {
		PkgsDirs []string `json:"pkgs_dirs"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", fmt.Errorf("conda: %v", err)
	}
	if len(info.PkgsDirs) == 0 {
		return "", errors.New("conda: no pkgs_dirs")
	}
	return absolute("conda", info.PkgsDirs[0])
}

// homeDir answers a fixed location below home; the macOS form is used on darwin.
func homeDir(rel ...string) func(*core.Env) (string, error) {
	return func(env *core.Env) (string, error) {
		if env.Home == "" {
			return "", errors.New("no home")
		}
		return filepath.Join(append([]string{env.Home}, rel...)...), nil
	}
}

func pipenvDir(env *core.Env) (string, error) {
	if env.Platform != nil && env.Platform.OS() == "darwin" {
		return homeDir("Library", "Caches", "pipenv")(env)
	}
	return homeDir(".cache", "pipenv")(env)
}

func add(id string, dir func(*core.Env) (string, error), actions map[string][]string) {
	registry[id] = Tool{ID: id, Dir: dir, Actions: actions}
	for a := range actions {
		core.RegisterToolAction(id, a)
	}
}

func init() {
	add("go", query("go", "env", "GOCACHE"), map[string][]string{"clean-cache": {"clean", "-cache"}})
	add("npm", query("npm", "config", "get", "cache"), map[string][]string{"clean-cache": {"cache", "clean", "--force"}})
	add("pnpm", query("pnpm", "store", "path"), map[string][]string{"prune": {"store", "prune"}})
	add("yarn", query("yarn", "cache", "dir"), map[string][]string{"clean-cache": {"cache", "clean"}})
	add("bun", homeDir(".bun", "install", "cache"), map[string][]string{"clean-cache": {"pm", "cache", "rm"}})
	add("pip", query("pip", "cache", "dir"), map[string][]string{"purge": {"cache", "purge"}})
	add("uv", query("uv", "cache", "dir"), map[string][]string{"prune": {"cache", "prune"}})
	add("dotnet", dotnetDir, map[string][]string{"clean-cache": {"nuget", "locals", "all", "--clear"}})
	add("brew", query("brew", "--cache"), map[string][]string{"cleanup": {"cleanup", "--prune=30"}})
	add("poetry", query("poetry", "config", "cache-dir"), map[string][]string{"clear": {"cache", "clear", "--all", "-n", "PyPI"}})
	add("pipenv", pipenvDir, map[string][]string{"clear": {"--clear"}})
	add("conda", condaDir, map[string][]string{"clean": {"clean", "--packages", "--tarballs", "-y"}})
}
