package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
)

func homeToolFix(t *testing.T) *fix {
	t.Helper()
	core.RegisterToolAction("faketool", "prune")
	f := newFix(t)
	f.run.Paths["faketool"] = "/bin/faketool"
	f.run.On([]string{"faketool", "prune"}, "", nil)
	return f
}

func homeCand(f *fix, dir string) core.Candidate {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return core.Candidate{Category: "temp", RuleKey: "x/a@1", Path: dir, Base: f.home, Removal: core.ToolCommand,
		Command: []string{"faketool", "prune"}, Bytes: -1}
}

func TestToolCommandBaseHomeAccepted(t *testing.T) {
	f := homeToolFix(t)
	c := homeCand(f, filepath.Join(f.home, ".npm"))
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if !hasPath(s.Removed, c.Path) {
		t.Fatalf("cache dir directly in home must be accepted: %+v", s)
	}
}

func TestToolCommandBaseHomeDeniedPathRefused(t *testing.T) {
	f := homeToolFix(t)
	c := homeCand(f, filepath.Join(f.home, "Documents"))
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if len(s.Removed) != 0 || keptWhy(s.Kept, c.Path) != "deny" {
		t.Fatalf("Documents must be refused by deny: %+v", s)
	}
}

func TestRenameDeleteBaseHomeRefused(t *testing.T) {
	f := newFix(t)
	p := filepath.Join(f.home, ".stuff")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	c := core.Candidate{Category: "temp", RuleKey: "x/a@1", Path: p, Base: f.home, Removal: core.RenameDelete, Bytes: -1}
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if len(s.Removed) != 0 || keptWhy(s.Kept, p) != "place" || !exists(p) {
		t.Fatalf("RenameDelete with Base=Home must be refused: %+v", s)
	}
}

func TestToolCommandWithoutPathBaseHomeNotSpecial(t *testing.T) {
	f := homeToolFix(t)
	c := homeCand(f, filepath.Join(f.home, ".npm"))
	c.Path = ""
	s := f.do(Options{}, f.cat("temp", core.Auto, c))
	if len(s.Removed) != 1 { // no Path: no base check applies at all
		t.Fatalf("%+v", s)
	}
}
