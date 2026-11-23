package caches

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/state"
)

func stampFile(t *testing.T) string {
	t.Helper()
	p, err := (&category{name: "mcp-runtime"}).stampPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func openStore(t *testing.T) *state.Store {
	t.Helper()
	p, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveConsent(&state.Consent{At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return st
}

// pruneFixture has uv on PATH and the mcp-runtime category ready to run.
func pruneFixture(t *testing.T) (*fixture, *category) {
	t.Helper()
	f := newFixture(t)
	f.tool(t, "uv", []string{"uv", "cache", "dir"}, f.dir("uv"), 10)
	return f, &category{name: "mcp-runtime"}
}

func TestDryRunPlanAndFailureLeaveNoStamp(t *testing.T) {
	f, cat := pruneFixture(t)
	f.fr.On([]string{"uv", "cache", "prune"}, "", nil)
	engine.Plan(f.env, []core.Category{cat}, engine.Options{})
	if _, err := engine.Run(f.env, []core.Category{cat}, openStore(t), engine.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stampFile(t)); err == nil {
		t.Fatal("a plan or a dry run must not record the weekly run")
	}
	f.fr.On([]string{"uv", "cache", "prune"}, "", errors.New("exit status 2"))
	s, err := engine.Run(f.env, []core.Category{cat}, openStore(t), engine.Options{})
	if err != nil || len(s.Failed) != 1 {
		t.Fatalf("want one failed removal: %+v %v", s, err)
	}
	if _, err := os.Stat(stampFile(t)); err == nil {
		t.Fatal("a failed command must not record the weekly run")
	}
}

func TestSuccessfulPruneWritesStamp(t *testing.T) {
	f, cat := pruneFixture(t)
	f.fr.On([]string{"uv", "cache", "prune"}, "", nil)
	s, err := engine.Run(f.env, []core.Category{cat}, openStore(t), engine.Options{})
	if err != nil || len(s.Removed) != 1 {
		t.Fatalf("want one removal: %+v %v", s, err)
	}
	p := stampFile(t)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("no stamp: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("stamp mode %v, want 0600", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(p))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("stamp dir mode %v, want 0700", di.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
	if again, _, _ := cat.Scan(f.env); len(again) != 0 {
		t.Fatalf("scan right after the prune: %v", keys(again))
	}
}

func TestStampInTheFutureCountsAsNeverRun(t *testing.T) {
	f, cat := pruneFixture(t)
	cat.AfterRemove(f.env, core.Candidate{Removal: core.ToolCommand})
	f.env.Now = f.env.Now.Add(-48 * time.Hour) // the clock went back: the stamp is now in the future
	if got, _, _ := cat.Scan(f.env); len(got) != 1 {
		t.Fatalf("a future stamp must not block the prune: %v", keys(got))
	}
}

func TestCachesCategoryAfterRemoveWritesNoStamp(t *testing.T) {
	f := newFixture(t)
	(&category{name: "caches"}).AfterRemove(f.env, core.Candidate{Removal: core.ToolCommand})
	if _, err := os.Stat(stampFile(t)); err == nil {
		t.Fatal("only mcp-runtime keeps a weekly stamp")
	}
}

func TestNpmDirAskedOncePerRun(t *testing.T) {
	f := newFixture(t)
	f.tool(t, "npm", []string{"npm", "config", "get", "cache"}, f.dir("npm"), 10)
	if err := os.MkdirAll(filepath.Join(f.dir("npm"), "_npx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&category{name: "caches"}).Scan(f.env); err != nil {
		t.Fatal(err)
	}
	lowDiskCandidates(f.env)
	n := 0
	for _, c := range f.fr.Calls {
		if len(c.Argv) > 1 && c.Argv[0] == "npm" && c.Argv[1] == "config" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("npm cache dir asked %d times in one run, want 1", n)
	}
}

func TestLowDiskStopsWhenContextDone(t *testing.T) {
	f := newFixture(t)
	f.tool(t, "go", []string{"go", "env", "GOCACHE"}, f.dir("go"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.env.Ctx = ctx
	if got := lowDiskCandidates(f.env); len(got) != 0 {
		t.Fatalf("a finished run budget must stop the hook: %v", keys(got))
	}
}

// conda keeps its packages in a non-hidden home folder (~/miniconda3/pkgs). The engine only accepts bases below
// a hidden home folder, a root or the temp dir, so such a cache is kept with "place" and conda clean is never
// run. That is by design: sapulidi does not clean what lives in an ordinary home folder.
func TestCondaInNonHiddenHomeFolderKeptPlace(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.home, "miniconda3", "pkgs")
	f.tool(t, "conda", nil, dir, 10)
	f.fr.On([]string{"conda", "info", "--json"}, `{"pkgs_dirs":["`+dir+`"]}`, nil)
	f.set.LowDisk = 1 << 40 // far above the free space the fake platform reports: the low-disk tier runs
	s, err := engine.Run(f.env, []core.Category{&category{name: "caches"}}, openStore(t), engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	why := ""
	for _, k := range s.Kept {
		if k.Path == dir {
			why = k.Why
		}
	}
	if why != "place" || len(s.Removed) != 0 {
		t.Fatalf("want kept place, got kept %+v removed %+v", s.Kept, s.Removed)
	}
	for _, c := range f.fr.Calls {
		if len(c.Argv) > 1 && c.Argv[0] == "conda" && c.Argv[1] == "clean" {
			t.Fatal("conda clean must not run")
		}
	}
}

func TestGradleDaemonLogsReadsDaemonDir(t *testing.T) {
	f := newFixture(t)
	if got := daemonLogCandidates(f.env, core.Rule{Category: "caches", MinAge: time.Hour}); len(got) != 0 {
		t.Fatalf("no daemon dir: %v", got)
	}
}
