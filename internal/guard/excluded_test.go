package guard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/exclude"
	"github.com/als0x/sapulidi/internal/testutil"
)

func TestExcluded(t *testing.T) {
	home := testutil.TempHome(t)
	list, err := exclude.Load(config.Paths{Config: filepath.Join(home, ".config", "sapulidi", "config.toml")})
	if err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(home, "proj", "node_modules", "keep-me")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(keep)
	if _, _, err := list.Add(keep, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	nm := filepath.Dir(real)
	other := filepath.Join(nm, "other")

	cases := []struct {
		name string
		c    core.Candidate
		want bool
	}{
		{"path: contains an exclusion", core.Candidate{Path: nm, Base: filepath.Dir(nm), Removal: core.RenameDelete}, true},
		{"path: the exclusion", core.Candidate{Path: real, Base: nm, Removal: core.RenameDelete}, true},
		{"path: sibling is fine", core.Candidate{Path: other, Base: nm, Removal: core.RenameDelete}, false},
		{"tool command reports an excluded dir", core.Candidate{Path: real, Base: nm, Removal: core.ToolCommand, Command: []string{"go", "clean"}}, true},
		{"tool command elsewhere", core.Candidate{Path: other, Base: nm, Removal: core.ToolCommand}, false},
		{"worktree remove", core.Candidate{Path: filepath.Join(real, "wt"), Base: real, Removal: core.WorktreeRemove, Dir: nm}, true},
		{"worktree elsewhere", core.Candidate{Path: other, Base: nm, Removal: core.WorktreeRemove, Dir: nm}, false},
		{"base inside an exclusion", core.Candidate{Path: filepath.Join(real, "a"), Base: real, Removal: core.RenameDelete}, true},
		{"base only inside, path outside lexically", core.Candidate{Path: other, Base: real, Removal: core.RenameDelete}, true},
		{"prune: Dir is the exclusion", core.Candidate{Removal: core.ToolCommand, Dir: real, Command: []string{"git"}}, true},
		{"prune: Dir contains an exclusion", core.Candidate{Removal: core.ToolCommand, Dir: nm, Command: []string{"git"}}, true},
		{"prune: Dir below an exclusion", core.Candidate{Removal: core.ToolCommand, Dir: filepath.Join(real, "x"), Command: []string{"git"}}, true},
		{"prune: Dir elsewhere", core.Candidate{Removal: core.ToolCommand, Dir: other, Command: []string{"git"}}, false},
		{"worktree remove: main repo is excluded", core.Candidate{Path: other, Base: nm, Removal: core.WorktreeRemove, Dir: real}, true},
		{"tool with path: Dir only contains an exclusion", core.Candidate{Path: other, Base: nm, Removal: core.ToolCommand, Dir: nm}, false},
		{"report only", core.Candidate{Path: real, Removal: core.ReportOnly}, true},
	}
	for _, c := range cases {
		err := Excluded(list, c.c)
		if !c.want {
			if err != nil {
				t.Errorf("%s: got %v", c.name, err)
			}
			continue
		}
		var ke *core.KeepError
		if !errors.As(err, &ke) || ke.Why != "excluded" || ke.Detail != real {
			t.Errorf("%s: got %v, want Keep(excluded, %s)", c.name, err, real)
		}
	}
	for _, c := range []core.Candidate{{Path: "rel/keep"}, {Path: real, Base: "rel"},
		{Removal: core.ToolCommand, Dir: "rel"}, {Path: other, Removal: core.WorktreeRemove, Dir: "rel"}} {
		var ke *core.KeepError
		if err := Excluded(list, c); !errors.As(err, &ke) || ke.Why != "excluded" || !strings.Contains(ke.Detail, "not absolute") {
			t.Errorf("relative %+v: %v", c, err)
		}
	}
	link := filepath.Join(real, "link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if err := Excluded(list, core.Candidate{Path: link, Base: nm}); err == nil {
		t.Error("a link inside an excluded folder must be kept")
	}
	if err := Excluded(list, core.Candidate{Path: other, Base: link}); err == nil {
		t.Error("a base reached through a link inside an excluded folder must be kept")
	}
	if err := Excluded(nil, core.Candidate{Path: real}); err != nil {
		t.Errorf("nil list: %v", err)
	}
}
