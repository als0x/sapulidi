package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	packsload "github.com/als0x/sapulidi/internal/packs"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/testutil"
	"github.com/als0x/sapulidi/packs"
)

func (f *fix) record(keys ...string) {
	f.t.Helper()
	if err := f.st.SaveConsent(&state.Consent{At: f.now, Rules: keys}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fix) consented() []string {
	f.t.Helper()
	c, err := f.st.Consent()
	if err != nil {
		f.t.Fatal(err)
	}
	if c == nil {
		return nil
	}
	return c.Rules
}

func TestApplyAddRuleRecordsConsent(t *testing.T) {
	f := newFix(t)
	f.record("x/a@1")
	p := Proposal{ID: "p1", Kind: "add-rule", Target: "my-out", Value: goodRule, Reason: "r"}
	tty := withTTY(t, "my-out\n")
	if _, err := Apply(ApplyDeps{Paths: f.paths}, p); err != nil {
		t.Fatal(err)
	}
	// the recorded key is the one the loader gives the new rule
	rules, _ := packsload.Load(packs.FS, filepath.Join(filepath.Dir(f.paths.Config), "packs"))
	i := slices.IndexFunc(rules, func(r core.Rule) bool { return r.ID == "my-out" })
	if i < 0 {
		t.Fatal("the rule was not loaded")
	}
	if want := []string{rules[i].Key(), "x/a@1"}; !slices.Equal(f.consented(), want) {
		t.Fatalf("%v, want %v", f.consented(), want)
	}
	if !strings.Contains(tty.out.String(), "schedule") {
		t.Errorf("the prompt does not say the schedule may use the rule:\n%s", tty.out.String())
	}
}

// Before setup there is no consent record, and approving a proposal does not create one.
func TestApplyAddRuleBeforeSetupRecordsNothing(t *testing.T) {
	f := newFix(t)
	withTTY(t, "my-out\n")
	p := Proposal{ID: "p1", Kind: "add-rule", Target: "my-out", Value: goodRule, Reason: "r"}
	if _, err := Apply(ApplyDeps{Paths: f.paths}, p); err != nil {
		t.Fatal(err)
	}
	if c, err := f.st.Consent(); err != nil || c != nil {
		t.Fatalf("%v %v", c, err)
	}
}

// userPack writes a user pack with one optional rule of deps: user-user/out@1.
func (f *fix) userPack() {
	f.t.Helper()
	dir := filepath.Join(filepath.Dir(f.paths.Config), "packs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	pack := "pack = \"user\"\n[[rule]]\nid = \"out\"\nrev = 1\ncategory = \"deps\"\nbucket = \"optional\"\n" +
		"kind = \"project\"\nnames = [\"out-dir\"]\nmarkers = [\"go.mod\"]\nmin_age = \"30d\"\n"
	if err := os.WriteFile(filepath.Join(dir, "user.toml"), []byte(pack), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func TestApplyEnableCategoryConsentNeedsTerminal(t *testing.T) {
	f := newFix(t)
	f.userPack()
	f.record()
	p := enableDeps()
	p.ID = "p1"
	d := ApplyDeps{Paths: f.paths, Yes: true}

	for name, setup := range map[string]func(){
		"no terminal": func() { withoutTTY(t) },
		"mismatch":    func() { withTTY(t, "no\n") },
	} {
		setup()
		res, err := Apply(d, p)
		if err != nil || len(f.consented()) != 0 || !strings.Contains(res.Message, "sapulidi consent --yes") {
			t.Fatalf("%s: %q %v %v", name, res.Message, err, f.consented())
		}
	}
	tty := withTTY(t, "allow\n")
	if _, err := Apply(d, p); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.consented(), "user-user/out@1") || !strings.Contains(tty.out.String(), "user-user/out@1") {
		t.Fatalf("%v\n%s", f.consented(), tty.out.String())
	}
}

func TestApplyCleanOnceConsentNeedsTerminal(t *testing.T) {
	f := newFix(t)
	tmp := filepath.Join(f.home, "tmp")
	old := filepath.Join(tmp, "old")
	testutil.Touch(t, filepath.Join(old, "data.bin"), 72*time.Hour)
	testutil.Touch(t, old, 72*time.Hour)
	f.record()
	cat := &fcat{name: "deps", bucket: core.Optional, cands: []core.Candidate{
		{Category: "deps", RuleKey: "x/deps@1", Path: old, Base: tmp, Removal: core.RenameDelete, Bytes: -1},
	}}
	env := &core.Env{
		Exclusions: testutil.NoExclusions,
		Home:       f.home, Now: time.Now(), TempDir: tmp, Ctx: context.Background(),
		Platform: &testutil.FakePlatform{Temp: tmp, Free: 1 << 40},
		Run:      f.run, Log: &testutil.MemLogger{},
		Settings: config.NewSettings(config.Defaults(), &config.Consent{}, map[string]core.Bucket{"deps": core.Optional}),
		Rules:    []core.Rule{{Pack: "x", ID: "deps", Rev: 1, Category: "deps", Bucket: core.Optional, Kind: core.KindProject, MinAge: 24 * time.Hour}},
	}
	p := Proposal{ID: "p1", Kind: "clean-once", Target: "deps", Reason: "r", Paths: []string{old}}
	d := ApplyDeps{Paths: f.paths, Yes: true, Env: env, Cats: []core.Category{cat}, Store: f.st}

	withoutTTY(t)
	if _, err := Apply(d, p); !errors.Is(err, confirm.ErrNoTerminal) {
		t.Fatalf("err %v, want ErrNoTerminal", err)
	}
	withTTY(t, "no\n")
	if _, err := Apply(d, p); !errors.Is(err, confirm.ErrMismatch) {
		t.Fatalf("err %v, want ErrMismatch", err)
	}
	if _, err := os.Stat(old); err != nil || len(f.consented()) != 0 {
		t.Fatalf("a refused proposal changed something: %v %v", err, f.consented())
	}
	tty := withTTY(t, "allow\n")
	res, err := Apply(d, p)
	if err != nil || !slices.Equal(res.Removed, []string{old}) {
		t.Fatalf("%+v %v", res, err)
	}
	if !slices.Equal(f.consented(), []string{"x/deps@1"}) {
		t.Fatalf("%v", f.consented())
	}
	for _, want := range []string{old, "x/deps@1", "schedule"} {
		if !strings.Contains(tty.out.String(), want) {
			t.Errorf("the prompt does not show %q:\n%s", want, tty.out.String())
		}
	}
}
