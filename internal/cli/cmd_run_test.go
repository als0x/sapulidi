package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/testutil"
)

type recorder struct {
	in *strings.Reader
	bytes.Buffer
}

func (r *recorder) Read(p []byte) (int, error) { return r.in.Read(p) }
func (r *recorder) Close() error               { return nil }

// recordingTTY is a fake terminal that answers typed and keeps what was written to it.
func recordingTTY(t *testing.T, typed string) *recorder {
	t.Helper()
	withTerminal(t, true, "") // restores both hooks at cleanup
	r := &recorder{in: strings.NewReader(typed)}
	confirm.OpenTTY = func() (io.ReadWriteCloser, error) { return r, nil }
	return r
}

// runFixture is a real plan over one old folder per label; the safe rule is consented.
func runFixture(t *testing.T) (safe, rec, call string) {
	t.Helper()
	home := testutil.TempHome(t)
	t.Setenv("SAPULIDI_TEST", "1")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	tmp := filepath.Join(home, "tmp")
	mk := func(name string) string {
		p := filepath.Join(tmp, name)
		writeFile(t, filepath.Join(p, "data.bin"), "data")
		testutil.Touch(t, filepath.Join(p, "data.bin"), 48*time.Hour)
		testutil.Touch(t, p, 48*time.Hour)
		return p
	}
	safe, rec, call = mk("safe"), mk("rec"), mk("call")
	env := &core.Env{
		Exclusions: testutil.NoExclusions, Home: home, Now: time.Now(), Ctx: context.Background(), TempDir: tmp,
		Platform: &testutil.FakePlatform{Temp: tmp, Free: 1 << 40},
		Run:      &testutil.FakeRunner{Paths: map[string]string{}},
		Log:      &testutil.MemLogger{},
		Settings: &testutil.FakeSettings{
			Categories:    map[string]bool{"temp": true, "deps": true, "worktrees": true},
			ConsentedKeys: map[string]bool{safeKey: true},
		},
		Rules: []core.Rule{
			{Pack: "x", ID: "safe", Rev: 1, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp, MinAge: time.Hour},
			{Pack: "x", ID: "rec", Rev: 1, Category: "deps", Bucket: core.Optional, Recommend: true, Kind: core.KindTemp, MinAge: 24 * time.Hour},
			{Pack: "y", ID: "call", Rev: 1, Category: "worktrees", Bucket: core.Optional, Kind: core.KindTemp, MinAge: 24 * time.Hour},
		},
	}
	cat := func(name string, b core.Bucket, key, path string) core.Category {
		return stubCat{name: name, bucket: b, cands: []core.Candidate{
			{Category: name, RuleKey: key, Path: path, Base: tmp, Removal: core.RenameDelete},
		}}
	}
	useBuild(t, env, []core.Category{
		cat("temp", core.Auto, safeKey, safe), cat("deps", core.Optional, recKey, rec),
		cat("worktrees", core.Optional, callKey, call),
	})
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveConsent(&config.Consent{At: time.Now().UTC(), Rules: []string{safeKey}}); err != nil {
		t.Fatal(err)
	}
	return safe, rec, call
}

func present(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestRunRecommendedRefusedWithoutTerminal(t *testing.T) {
	safe, rec, call := runFixture(t)
	withTerminal(t, false, "y\n")
	code, out, e := runCLI(t, "run", "--recommended")
	if code != 1 || !strings.Contains(e, "run this in your own terminal: sapulidi run --recommended") {
		t.Fatalf("%d %q %q", code, out, e)
	}
	code, out, _ = runCLI(t, "--json", "run", "--recommended")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 1 || doc["error"] == nil {
		t.Fatalf("%d %q %v", code, out, err)
	}
	if !present(safe) || !present(rec) || !present(call) {
		t.Fatal("a refused run removed something")
	}
}

func TestRunRecommendedAnswerNo(t *testing.T) {
	safe, rec, call := runFixture(t)
	withTerminal(t, true, "n\n")
	code, out, e := runCLI(t, "run", "--recommended")
	if code != 1 || !strings.Contains(e, "not confirmed, nothing was removed") {
		t.Fatalf("%d %q %q", code, out, e)
	}
	if !present(safe) || !present(rec) || !present(call) {
		t.Fatal("answering no removed something")
	}
}

func TestRunRecommendedRemovesOnlyRecommendedRows(t *testing.T) {
	safe, rec, call := runFixture(t)
	tty := recordingTTY(t, "y\n")
	code, out, e := runCLI(t, "run", "--recommended")
	if code != 0 || !strings.Contains(out, "removed 1 ") {
		t.Fatalf("%d %q %q", code, out, e)
	}
	if present(rec) || !present(safe) || !present(call) {
		t.Fatalf("rec %v safe %v call %v", present(rec), present(safe), present(call))
	}
	q := tty.String()
	if !strings.Contains(q, "recommended: 1 item(s), ") || !strings.Contains(q, rec) || !strings.HasSuffix(q, "[y/N] ") ||
		strings.Contains(q, call) || strings.Contains(q, safe) {
		t.Fatalf("question %q", q)
	}
}

func TestRunRecommendedWithNothingToRemove(t *testing.T) {
	safe, rec, call := runFixture(t)
	if err := os.Rename(rec, rec+".moved"); err != nil {
		t.Fatal(err)
	}
	cats := categories()
	categories = func() []core.Category { return []core.Category{cats[0], cats[2]} }
	tty := recordingTTY(t, "y\n")
	code, out, e := runCLI(t, "run", "--recommended")
	if code != 0 || !strings.Contains(out, "no recommended rows to remove") || tty.Len() != 0 {
		t.Fatalf("%d %q %q %q", code, out, e, tty.String())
	}
	if !present(safe) || !present(call) {
		t.Fatal("an empty selection removed something")
	}
}

// A path-less recommended row cannot be named in Options.Paths, so the question must not count it.
func TestRunRecommendedPromisesOnlyRowsWithAPath(t *testing.T) {
	safe, rec, call := runFixture(t)
	env, _ := BuildEnv()
	tmp := env.TempDir
	rows := []core.Candidate{{Category: "deps", RuleKey: recKey, Path: rec, Base: tmp, Removal: core.RenameDelete}}
	for _, name := range []string{"rec2", "rec3"} {
		p := filepath.Join(tmp, name)
		writeFile(t, filepath.Join(p, "data.bin"), "data")
		testutil.Touch(t, filepath.Join(p, "data.bin"), 48*time.Hour)
		testutil.Touch(t, p, 48*time.Hour)
		rows = append(rows, core.Candidate{Category: "deps", RuleKey: recKey, Path: p, Base: tmp, Removal: core.RenameDelete})
	}
	categories = func() []core.Category {
		return []core.Category{stubCat{name: "deps", bucket: core.Optional, cands: rows}}
	}
	oldPlan := planFn
	planFn = func(env *core.Env, cats []core.Category, opt engine.Options) engine.Summary {
		s := oldPlan(env, cats, opt)
		s.WouldRemove = append(s.WouldRemove, core.Candidate{Category: "deps", RuleKey: recKey, Bytes: 1 << 30,
			Label: core.LabelRecommended})
		return s
	}
	t.Cleanup(func() { planFn = oldPlan })
	tty := recordingTTY(t, "y\n")
	code, out, e := runCLI(t, "run", "--recommended")
	if code != 0 || !strings.Contains(out, "removed 3 ") {
		t.Fatalf("%d %q %q", code, out, e)
	}
	if q := tty.String(); !strings.Contains(q, "recommended: 3 item(s), ") || strings.Contains(q, "GiB") {
		t.Fatalf("question %q", q)
	}
	for _, r := range rows {
		if present(r.Path) {
			t.Errorf("%s was shown but not removed", r.Path)
		}
	}
	if !present(safe) || !present(call) {
		t.Fatal("a row that was not shown was removed")
	}
}

func TestRunRecommendedDryRunAsksNothingAndRemovesNothing(t *testing.T) {
	safe, rec, call := runFixture(t)
	tty := recordingTTY(t, "y\n")
	code, out, e := runCLI(t, "run", "--recommended", "--dry-run")
	if code != 0 || !strings.Contains(out, "recommended: 1 item(s), ") || !strings.Contains(out, rec) ||
		!strings.Contains(out, "dry run, 1 would be removed") || strings.Contains(out, "[y/N]") {
		t.Fatalf("%d %q %q", code, out, e)
	}
	code, out, e = runCLI(t, "--json", "run", "--recommended", "--dry-run")
	var doc struct {
		Mode        string `json:"mode"`
		Removed     int    `json:"removed"`
		WouldRemove int    `json:"would_remove"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 || doc.Mode != "dry-run" ||
		doc.Removed != 0 || doc.WouldRemove != 1 {
		t.Fatalf("%d %q %q %v", code, out, e, err)
	}
	if tty.Len() != 0 {
		t.Fatalf("a dry run asked %q", tty.String())
	}
	if !present(safe) || !present(rec) || !present(call) {
		t.Fatal("a dry run removed something")
	}
}

func TestRecommendedPromptShowsFirstRows(t *testing.T) {
	var cs []core.Candidate
	for i := range 13 {
		cs = append(cs, core.Candidate{Category: "deps", Path: "/work/a\x1b" + string(rune('a'+i)), Bytes: 1024})
	}
	got := recommendedPrompt(cs)
	for _, want := range []string{"recommended: 13 item(s), 13.0 KiB", "/work/a?a", "/work/a?j", "and 3 more"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "/work/a?k") || strings.Contains(got, "\x1b") {
		t.Errorf("too many rows or a raw control: %q", got)
	}
}
