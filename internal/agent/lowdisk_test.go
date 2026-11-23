package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/testutil"
)

// A clean-once proposal sets Paths and Only, so the engine's low-disk tier (which removes largest first, outside
// the listed paths) must never run. SetLowDisk may be set once per process: this is the only test that sets it.
func TestCleanOnceNeverRunsLowDiskTier(t *testing.T) {
	f := newFix(t)
	tmp := filepath.Join(f.home, "tmp")
	victim := filepath.Join(tmp, "victim")
	other := filepath.Join(tmp, "other")
	for _, p := range []string{victim, other} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(p, "data.bin")
		if err := os.WriteFile(file, make([]byte, 4000), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.Touch(t, file, 72*time.Hour)
		testutil.Touch(t, p, 72*time.Hour)
	}
	cat := &fcat{name: "deps", bucket: core.Optional, cands: []core.Candidate{
		{Category: "deps", RuleKey: "x/deps@1", Path: victim, Base: tmp, Removal: core.RenameDelete, Bytes: -1},
	}}
	if err := f.st.SaveConsent(&state.Consent{At: f.now}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	engine.SetLowDisk(func(*core.Env, *engine.Summary) []core.Candidate {
		calls++
		return []core.Candidate{{Category: "deps", RuleKey: "x/deps@1", Path: other, Base: tmp, Removal: core.RenameDelete, Bytes: -1}}
	})
	env := &core.Env{
		Exclusions: testutil.NoExclusions,
		Home:       f.home, Now: time.Now(), TempDir: tmp, Ctx: context.Background(),
		Platform: &testutil.FakePlatform{Temp: tmp, Free: 1 << 20}, // far below the limit
		Run:      f.run, Log: &testutil.MemLogger{},
		Settings: &testutil.FakeSettings{ConsentAll: true, LowDisk: 1 << 50, Categories: map[string]bool{"deps": true}},
		Rules:    []core.Rule{{Pack: "x", ID: "deps", Rev: 1, Category: "deps", Bucket: core.Optional, Kind: core.KindProject, MinAge: 24 * time.Hour}},
	}
	p := Proposal{ID: "p1", Kind: "clean-once", Target: "deps", Reason: "r", Paths: []string{victim}}
	d := ApplyDeps{Paths: f.paths, Env: env, Cats: []core.Category{cat}, Store: f.st, Yes: true}
	if _, err := Apply(d, p); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("the low-disk tier ran %d time(s) for a clean-once", calls)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("a path outside the proposal was removed")
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatal("the listed path was not removed")
	}

	// Control: a full run with the same setup does call the hook, so the check above can fail.
	if _, err := engine.Run(env, []core.Category{cat}, f.st, engine.Options{}); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("control: the low-disk hook is not reachable in this setup, so the test proves nothing")
	}
}
