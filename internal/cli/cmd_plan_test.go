package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/testutil"
)

// stubCat is a category that returns the given candidates.
type stubCat struct {
	name   string
	bucket core.Bucket
	cands  []core.Candidate
}

func (c stubCat) Name() string        { return c.name }
func (c stubCat) Bucket() core.Bucket { return c.bucket }
func (c stubCat) Scan(*core.Env) ([]core.Candidate, []core.Kept, error) {
	return append([]core.Candidate(nil), c.cands...), nil, nil
}
func (c stubCat) Proof(*core.Env, core.Candidate) error { return nil }

func useBuild(t *testing.T, env *core.Env, cats []core.Category) {
	t.Helper()
	oldBuild, oldCats := BuildEnv, categories
	BuildEnv = func() (*core.Env, error) { return env, nil }
	categories = func() []core.Category { return cats }
	t.Cleanup(func() { BuildEnv, categories = oldBuild, oldCats })
}

// labelFixture is a fixed plan holding every label, an agent rule of each agent, an expanded root and kept rows.
func labelFixture(t *testing.T) {
	t.Helper()
	testutil.TempHome(t)
	t.Setenv("SAPULIDI_TEST", "1")
	withTerminal(t, false, "")
	env := &core.Env{
		Ctx: context.Background(),
		Roots: []core.Root{
			{Path: "/work/app", Source: "claude"},
			{Path: "/work/mono/lib", Source: "claude+children"},
		},
		Rules: []core.Rule{
			{Pack: "temp", ID: "go-build", Rev: 1, Category: "temp", Bucket: core.Auto},
			{Pack: "rust", ID: "target", Rev: 2, Category: "deps", Bucket: core.Optional, Recommend: true},
			{Pack: "node", ID: "dist", Rev: 1, Category: "deps", Bucket: core.Optional},
			{Pack: "agent", ID: "claude-scratch", Rev: 1, Category: "agent-scratch", Bucket: core.Auto, Location: "claude-scratch"},
			{Pack: "agent", ID: "codex-sessions", Rev: 1, Category: "agent-transcripts", Bucket: core.Optional, Location: "codex-sessions"},
		},
		Settings: &testutil.FakeSettings{Categories: map[string]bool{
			"temp": true, "deps": true, "agent-scratch": true, "agent-transcripts": false,
		}},
	}
	useBuild(t, env, []core.Category{
		stubCat{name: "temp", bucket: core.Auto}, stubCat{name: "deps", bucket: core.Optional},
		stubCat{name: "agent-scratch", bucket: core.Auto}, stubCat{name: "agent-transcripts", bucket: core.Optional},
	})
	cand := func(cat, key, path string, bytes int64, label core.Label, reason string) core.Candidate {
		return core.Candidate{Category: cat, RuleKey: key, Path: path, Bytes: bytes, Label: label, Reason: reason, Source: "auto"}
	}
	sum := engine.Summary{
		Mode: "dry-run", FreeBefore: 1000,
		WouldRemove: []core.Candidate{
			cand("deps", "node/dist@1", "/work/app/dist", 8192, core.LabelYourCall, "build output"),
			cand("temp", "temp/go-build@1", "/tmp/go-build1", 1024, core.LabelSafe, "old go build folder"),
			cand("deps", "rust/target@2", "/work/mono/lib/target", 4096, core.LabelRecommended, "rust build output"),
			cand("agent-scratch", "agent/claude-scratch@1", "/tmp/claude/s1", 2048, core.LabelSafe, "scratch of an old session"),
			cand("agent-transcripts", "agent/codex-sessions@1", "/srv/me/.codex/sessions/a", 16384, core.LabelYourCall, "old session"),
		},
		Kept: []core.Kept{
			{Category: "temp", RuleKey: "temp/go-build@1", Path: "/tmp/go-build2", Why: "young"},
			{Category: "temp", RuleKey: "temp/go-build@1", Path: "/tmp/go-build3", Why: "young"},
			{Category: "deps", RuleKey: "rust/target@2", Path: "/work/app/target", Why: "open"},
		},
		Waiting: []string{"rust/target@2"},
	}
	oldPlan := planFn
	planFn = func(*core.Env, []core.Category, engine.Options) engine.Summary { return sum }
	t.Cleanup(func() { planFn = oldPlan })
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if got != string(want) {
		t.Fatalf("%s differs\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestPlanGroupsByLabel(t *testing.T) {
	labelFixture(t)
	code, out, e := runCLI(t, "plan")
	if code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	golden(t, "plan.golden", out)
}

func TestPlanAllListsProtectedRows(t *testing.T) {
	labelFixture(t)
	code, out, e := runCLI(t, "plan", "--all")
	if code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	golden(t, "plan-all.golden", out)
}

func TestPlanEscapesPaths(t *testing.T) {
	labelFixture(t)
	const evil = "/work/evil\x1b[2K‮"
	planFn = func(*core.Env, []core.Category, engine.Options) engine.Summary { // labelFixture restores it
		return engine.Summary{
			Mode:        "dry-run",
			WouldRemove: []core.Candidate{{Category: "temp", Path: evil + "/gone", Label: core.LabelSafe}},
			Kept:        []core.Kept{{Category: "temp", Path: evil + "/kept", Why: "young"}},
		}
	}
	code, out, e := runCLI(t, "plan", "--all")
	if code != 0 || strings.ContainsAny(out, "\x1b‮") ||
		!strings.Contains(out, "/work/evil?[2K?/gone") || !strings.Contains(out, "/work/evil?[2K?/kept") {
		t.Fatalf("%d %q %q", code, out, e)
	}
}

// The 0.2.0 document is what the new one must be once "label" is taken out of every candidate.
func TestPlanJSONAddsLabelOnly(t *testing.T) {
	labelFixture(t)
	code, out, e := runCLI(t, "--json", "plan")
	if code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	var got, want map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	old, err := os.ReadFile(filepath.Join("testdata", "plan-0.2.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(old, &want); err != nil {
		t.Fatal(err)
	}
	var labels []any
	for _, c := range got["candidates"].([]any) {
		m := c.(map[string]any)
		labels = append(labels, m["label"])
		delete(m, "label")
	}
	if wantLabels := []any{"your-call", "safe", "recommended", "safe", "your-call"}; !reflect.DeepEqual(labels, wantLabels) {
		t.Fatalf("labels %v, want %v", labels, wantLabels)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("old fields changed:\n%s", out)
	}
}
