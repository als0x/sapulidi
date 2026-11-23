package tools

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

func envWith(t *testing.T, run core.Runner) *core.Env {
	t.Helper()
	home := testutil.TempHome(t)
	return &core.Env{Home: home, Run: run, Ctx: context.Background()}
}

// blockRunner never answers until its context is done.
type blockRunner struct{}

func (blockRunner) Run(ctx context.Context, dir string, argv ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (blockRunner) LookPath(name string) (string, error) { return "/bin/" + name, nil }

func TestDirQueryTimeout(t *testing.T) {
	old := QueryTimeout
	QueryTimeout = 30 * time.Millisecond
	defer func() { QueryTimeout = old }()
	tool, ok := Get("go")
	if !ok {
		t.Fatal("no go tool")
	}
	start := time.Now()
	if _, err := tool.Dir(envWith(t, blockRunner{})); err == nil {
		t.Fatal("a hung query must be an error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("query took %s", time.Since(start))
	}
}

func TestDirQueries(t *testing.T) {
	fr := &testutil.FakeRunner{}
	fr.On([]string{"go", "env", "GOCACHE"}, "/c/go-build\n", nil)
	fr.On([]string{"npm", "config", "get", "cache"}, "/c/npm\n", nil)
	fr.On([]string{"pnpm", "store", "path"}, "/c/pnpm/v3\n", nil)
	fr.On([]string{"yarn", "cache", "dir"}, "/c/yarn\n", nil)
	fr.On([]string{"pip", "cache", "dir"}, "/c/pip\n", nil)
	fr.On([]string{"uv", "cache", "dir"}, "/c/uv\n", nil)
	fr.On([]string{"brew", "--cache"}, "/c/brew\n", nil)
	fr.On([]string{"poetry", "config", "cache-dir"}, "/c/poetry\n", nil)
	fr.On([]string{"dotnet", "nuget", "locals", "all", "--list"},
		"http-cache: /c/dn/http\nglobal-packages: /c/dn/pkgs\ntemp: /c/dn/tmp\n", nil)
	fr.On([]string{"conda", "info", "--json"}, `{"pkgs_dirs":["/c/conda/pkgs","/other"]}`, nil)
	env := envWith(t, fr)
	want := map[string]string{
		"go": "/c/go-build", "npm": "/c/npm", "pnpm": "/c/pnpm/v3", "yarn": "/c/yarn", "pip": "/c/pip",
		"uv": "/c/uv", "brew": "/c/brew", "poetry": "/c/poetry", "dotnet": "/c/dn/pkgs", "conda": "/c/conda/pkgs",
		"bun": filepath.Join(env.Home, ".bun", "install", "cache"),
	}
	for id, w := range want {
		tool, ok := Get(id)
		if !ok {
			t.Fatalf("no tool %s", id)
		}
		got, err := tool.Dir(env)
		if err != nil || got != w {
			t.Errorf("%s: dir = %q, %v; want %q", id, got, err, w)
		}
	}
}

func TestEveryToolActionRegistered(t *testing.T) {
	ids := IDs()
	if len(ids) != 12 {
		t.Fatalf("want 12 tools, got %v", ids)
	}
	for _, id := range ids {
		tool, _ := Get(id)
		if len(tool.Actions) == 0 || tool.Dir == nil {
			t.Errorf("%s: no actions or no Dir", id)
		}
		for action := range tool.Actions {
			if !core.ToolActionKnown(id, action) {
				t.Errorf("%s/%s is not registered", id, action)
			}
		}
	}
	if _, ok := Get("rm"); ok {
		t.Error("unknown tool must not be found")
	}
}
