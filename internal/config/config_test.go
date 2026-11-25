package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasWarn(w []string, sub string) bool {
	for _, s := range w {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestResolvePathsDefaultsAndOverrides(t *testing.T) {
	home := testutil.TempHome(t)
	for _, k := range []string{"SAPULIDI_CONFIG", "SAPULIDI_STATE", "SAPULIDI_DATA"} {
		t.Setenv(k, "")
	}
	p, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Config: filepath.Join(home, ".config", "sapulidi", "config.toml"),
		State:  filepath.Join(home, ".local", "state", "sapulidi"),
		Data:   filepath.Join(home, ".local", "share", "sapulidi"),
	}
	if p != want {
		t.Fatalf("got %+v want %+v", p, want)
	}
	// no XDG: falls back to HOME
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(k, "")
	}
	if p, _ = ResolvePaths(); p != want {
		t.Fatalf("no XDG: got %+v want %+v", p, want)
	}
	// overrides: a file path for config, directories for the others
	t.Setenv("SAPULIDI_CONFIG", filepath.Join(home, "x", "my.toml"))
	t.Setenv("SAPULIDI_STATE", filepath.Join(home, "st"))
	t.Setenv("SAPULIDI_DATA", filepath.Join(home, "da"))
	p, err = ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if p.Config != filepath.Join(home, "x", "my.toml") || p.State != filepath.Join(home, "st") || p.Data != filepath.Join(home, "da") {
		t.Fatalf("overrides: %+v", p)
	}
	// a config override naming a directory means <dir>/config.toml
	t.Setenv("SAPULIDI_CONFIG", home)
	if p, _ = ResolvePaths(); p.Config != filepath.Join(home, "config.toml") {
		t.Fatalf("dir override: %q", p.Config)
	}
}

func TestResolvePathsRejectsRelative(t *testing.T) {
	testutil.TempHome(t)
	for _, k := range []string{"SAPULIDI_CONFIG", "SAPULIDI_STATE", "SAPULIDI_DATA"} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, "relative/dir")
			if _, err := ResolvePaths(); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	c, w, err := Load(filepath.Join(dir, "config.toml"))
	if err != nil || len(w) != 0 {
		t.Fatalf("err=%v warns=%v", err, w)
	}
	if c.LowDiskGB != 15 || c.GrowthGBPerWeek != 5 || c.Agent.Enabled || c.Agent.CLI != "" ||
		c.Agent.Interval.D() != 7*24*time.Hour || c.Agent.MaxUSD != 0.25 || c.Notify.Enabled {
		t.Fatalf("defaults wrong: %+v", c)
	}
}

func TestLoadUnknownKeyWarns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	write(t, path, "low_disk_gb = 20\nbogus = 1\n[agent]\nmax_usd = 1.5\nwhat = true\n[category.deps]\ncolor = \"red\"\nenabled = true\n")
	c, w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bogus", "agent.what", "category.deps.color"} {
		if !hasWarn(w, k) {
			t.Errorf("no warning naming %q in %v", k, w)
		}
	}
	// the rest of the file still counts
	if c.LowDiskGB != 20 || c.Agent.MaxUSD != 1.5 || c.Categories["deps"].Enabled == nil || !*c.Categories["deps"].Enabled {
		t.Fatalf("valid keys lost: %+v", c)
	}
	if c.GrowthGBPerWeek != 5 {
		t.Fatalf("default lost")
	}
}

func TestLoadAgeBelowMinimumUsesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	write(t, path, "[category.temp]\nage = \"10m\"\n[category.deps]\nage = \"2h\"\n[category.build-output]\nage = \"5d\"\n[category.crash]\nage = \"abc\"\n")
	c, w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarn(w, "category.temp.age") || !hasWarn(w, "category.crash.age") {
		t.Fatalf("warnings: %v", w)
	}
	if c.Categories["temp"].Age != 0 {
		t.Fatal("age below 1h must be dropped")
	}
	// 2h passes Load (absolute floor) but an optional category needs 24h
	buckets := map[string]core.Bucket{"temp": core.Auto, "deps": core.Optional, "build-output": core.Optional}
	if !hasWarn(c.AgeWarnings(buckets), "category.deps.age") {
		t.Fatalf("AgeWarnings: %v", c.AgeWarnings(buckets))
	}
	s := NewSettings(c, nil, buckets)
	if got := s.Age("deps", 30*24*time.Hour); got != 30*24*time.Hour {
		t.Fatalf("optional 2h: got %v want default", got)
	}
	if got := s.Age("build-output", 7*24*time.Hour); got != 5*24*time.Hour {
		t.Fatalf("5d: got %v", got)
	}
	if got := s.Age("temp", 3*time.Hour); got != 3*time.Hour {
		t.Fatalf("dropped age: got %v", got)
	}
}

func TestLoadRelativeRootDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	write(t, path, "roots = [\"/abs/one\", \"rel/two\", \"~/three\"]\n")
	c, w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Roots) != 1 || c.Roots[0] != "/abs/one" {
		t.Fatalf("roots: %v", c.Roots)
	}
	if !hasWarn(w, "rel/two") || !hasWarn(w, "~/three") {
		t.Fatalf("warnings: %v", w)
	}
}

func TestLoadSyntaxErrorIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "low_disk_gb = [")
	c, w, err := Load(path)
	if err != nil || c == nil {
		t.Fatalf("Load must return a Config and no error: %v %v", c, err)
	}
	if len(c.Invalid) != 1 || c.Invalid[0] != path || !hasWarn(w, "not valid TOML") {
		t.Fatalf("invalid=%v warns=%v", c.Invalid, w)
	}
	// must not crash
	_ = c.Effective()
	_ = c.AgeWarnings(testBuckets)
	s := NewSettings(c, &Consent{Rules: []string{"go/go-build@1"}}, testBuckets)
	if s.CategoryEnabled("temp") || s.CategoryEnabled("deps") || s.Consented("go/go-build@1") {
		t.Fatal("corrupt config must fail closed")
	}
}

func TestCorruptAppliedKeepsGoodValuesButRunsNothing(t *testing.T) {
	p := paths(t)
	write(t, p.Config, "low_disk_gb = 20\n[category.deps]\nenabled = true\n")
	write(t, AppliedPath(p.Config), "[category.deps\nenabled = false")
	c, w, err := Load(p.Config)
	if err != nil || c == nil {
		t.Fatalf("%v %v", c, err)
	}
	if c.LowDiskGB != 20 || len(c.Invalid) != 1 || !hasWarn(w, "applied.toml") {
		t.Fatalf("good values must stay: %+v %v", c, w)
	}
	s := NewSettings(c, &Consent{Rules: []string{"go/go-build@1"}}, testBuckets)
	if s.CategoryEnabled("deps") || s.CategoryEnabled("temp") || s.Consented("go/go-build@1") {
		t.Fatal("nothing may run while a file is corrupt")
	}
	if s.LowDiskBytes() != 20<<30 {
		t.Fatal("other settings still come from the good file")
	}
}

func TestNewSettingsNilDoesNotCrash(t *testing.T) {
	s := NewSettings(nil, nil, nil)
	_ = s.CategoryEnabled("temp")
	_ = s.Age("temp", time.Hour)
	if s.Consented("x/y@1") || s.LowDiskBytes() != 15<<30 {
		t.Fatal("defaults expected")
	}
}

func TestNeverCategoryMissingFromBuckets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "[category.models]\nenabled = true\nage = \"30d\"\n[category.mystery]\nenabled = true\n")
	c, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSettings(c, nil, map[string]core.Bucket{"temp": core.Auto})
	if s.CategoryEnabled("models") {
		t.Fatal("never category enabled")
	}
	if got := s.Age("models", 5*time.Hour); got != 5*time.Hour {
		t.Fatalf("never category got an age: %v", got)
	}
	if !s.CategoryEnabled("mystery") {
		t.Fatal("unknown category is optional: on with enabled=true")
	}
	if NewSettings(Defaults(), nil, nil).CategoryEnabled("mystery") {
		t.Fatal("unknown category is off by default")
	}
}

func TestDurationParse(t *testing.T) {
	ok := map[string]time.Duration{
		"90m": 90 * time.Minute, "3h": 3 * time.Hour, "30d": 30 * 24 * time.Hour, "2w": 14 * 24 * time.Hour,
	}
	for s, want := range ok {
		d, err := ParseDuration(s)
		if err != nil || d.D() != want {
			t.Errorf("%s: %v %v", s, d, err)
		}
	}
	for _, s := range []string{"-1d", "abc", "", "0d", "5", "1.5h", "3x", "d"} {
		if _, err := ParseDuration(s); err == nil {
			t.Errorf("%q must be rejected", s)
		}
	}
	if Duration(30*24*time.Hour).String() != "30d" || Duration(90*time.Minute).String() != "90m" {
		t.Error("String")
	}
}

var testBuckets = map[string]core.Bucket{
	"temp": core.Auto, "deps": core.Optional, "models": core.Never,
}

func settingsFor(t *testing.T, body string, consent *Consent) core.Settings {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, body)
	c, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return NewSettings(c, consent, testBuckets)
}

func TestSettingsNeverCategoryCannotBeEnabled(t *testing.T) {
	s := settingsFor(t, "[category.models]\nenabled = true\n", nil)
	if s.CategoryEnabled("models") {
		t.Fatal("never category enabled by config")
	}
}

func TestSettingsOptionalOnByDefault(t *testing.T) { // CG1: was ...OffByDefault
	s := settingsFor(t, "[category.temp]\nenabled = false\n", nil)
	if s.CategoryEnabled("temp") {
		t.Fatal("auto category with enabled=false must be off")
	}
	if !s.CategoryEnabled("deps") { // CG1: an optional category is on unless switched off
		t.Fatal("optional category must be on by default")
	}
	s = settingsFor(t, "[category.deps]\nenabled = true\n", nil)
	if !s.CategoryEnabled("deps") || !s.CategoryEnabled("temp") {
		t.Fatal("optional enabled=true on, auto on by default")
	}
	if s.CategoryEnabled("undeclared") {
		t.Fatal("unknown category must be off")
	}
	if s.LowDiskBytes() != 15<<30 || s.GrowthBytesPerWeek() != 5<<30 || s.NotifyEnabled() {
		t.Fatal("derived values")
	}
}

func TestSettingsRuleDisabledAnyRev(t *testing.T) {
	s := settingsFor(t, "[rule.\"go/go-build\"]\nenabled = false\n", nil)
	for _, k := range []string{"go/go-build@1", "go/go-build@7"} {
		if s.RuleEnabled(k) {
			t.Errorf("%s must be disabled", k)
		}
	}
	if !s.RuleEnabled("go/other@1") {
		t.Error("other rule must stay enabled")
	}
}

func TestConsentedExactRevisionOnly(t *testing.T) {
	s := settingsFor(t, "", &Consent{Rules: []string{"go/go-build@1"}})
	if !s.Consented("go/go-build@1") || s.Consented("go/go-build@2") || s.Consented("go/other@1") {
		t.Fatal("consent must match the exact key")
	}
	if settingsFor(t, "", nil).Consented("go/go-build@1") {
		t.Fatal("nothing consented before setup")
	}
}

func paths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	return Paths{Config: filepath.Join(dir, "config.toml"), State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
}

func TestApplyWritesAppliedNotConfig(t *testing.T) {
	p := paths(t)
	orig := "# my comment\nlow_disk_gb = 20\n"
	write(t, p.Config, orig)
	if err := Apply(p, Change{"category.deps.enabled", "true"}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, Change{"category.deps.age", "45d"}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, Change{"rule.go/go-build.enabled", "false"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.Config); string(b) != orig {
		t.Fatalf("config.toml changed: %q", b)
	}
	fi, err := os.Stat(AppliedPath(p.Config))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("applied.toml: %v %v", fi, err)
	}
	c, w, err := Load(p.Config)
	if err != nil || len(w) != 0 {
		t.Fatalf("%v %v", err, w)
	}
	if e := c.Categories["deps"].Enabled; e == nil || !*e || c.Categories["deps"].Age.D() != 45*24*time.Hour {
		t.Fatalf("deps: %+v", c.Categories["deps"])
	}
	if e := c.Rules["go/go-build"].Enabled; e == nil || *e {
		t.Fatalf("rule: %+v", c.Rules)
	}
	if c.LowDiskGB != 20 {
		t.Fatal("config.toml value lost")
	}
}

func TestAppliedOverridesConfig(t *testing.T) {
	p := paths(t)
	write(t, p.Config, "[category.deps]\nenabled = true\nage = \"30d\"\n")
	if err := Apply(p, Change{"category.deps.enabled", "false"}); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Categories["deps"].Enabled; e == nil || *e {
		t.Fatal("applied must win")
	}
	if c.Categories["deps"].Age.D() != 30*24*time.Hour {
		t.Fatal("untouched key must keep the config.toml value")
	}
}

func TestApplyRejectsUnknownKeyAndNeverCategory(t *testing.T) {
	p := paths(t)
	bad := []Change{
		{"low_disk_gb", "1"},
		{"category.deps.color", "red"},
		{"category.models.enabled", "true"},
		{"category.deps.enabled", "yes"},
		{"category.deps.age", "5m"},
		{"category.deps.age", "abc"},
		{"rule.nopack.enabled", "true"},
		{"rule.go/x@1.enabled", "true"},
		{"category..enabled", "true"},
	}
	for _, c := range bad {
		if err := Apply(p, c); err == nil {
			t.Errorf("%+v must be rejected", c)
		}
	}
	if _, err := os.Stat(AppliedPath(p.Config)); err == nil {
		t.Fatal("rejected changes must not create applied.toml")
	}
}

func TestEffectiveNamesSource(t *testing.T) {
	p := paths(t)
	write(t, p.Config, "low_disk_gb = 20\n[category.deps]\nenabled = true\n")
	if err := Apply(p, Change{"category.deps.enabled", "false"}); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, v := range c.Effective() {
		got[v.Key] = [2]string{v.Value, v.Source}
	}
	want := map[string][2]string{
		"low_disk_gb":           {"20", SourceConfig},
		"growth_gb_per_week":    {"5", SourceDefault},
		"category.deps.enabled": {"false", SourceApplied},
		"notify.enabled":        {"false", SourceDefault},
		"update.check":          {"true", SourceDefault},
		"update.auto":           {"false", SourceDefault},
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: got %v want %v", k, got[k], w)
		}
	}
}

func TestLoadUpdateKeys(t *testing.T) {
	p := paths(t)
	c, warns, err := Load(p.Config)
	if err != nil || len(warns) != 0 || !c.Update.Check || c.Update.Auto {
		t.Fatalf("defaults: %+v %v %v", c.Update, warns, err)
	}
	write(t, p.Config, "[update]\ncheck = false\nauto = true\n")
	c, warns, err = Load(p.Config)
	if err != nil || len(warns) != 0 || c.Update.Check || !c.Update.Auto {
		t.Fatalf("set: %+v %v %v", c.Update, warns, err)
	}
	for _, v := range c.Effective() {
		if (v.Key == "update.check" && (v.Value != "false" || v.Source != SourceConfig)) ||
			(v.Key == "update.auto" && (v.Value != "true" || v.Source != SourceConfig)) {
			t.Errorf("effective: %+v", v)
		}
	}
	write(t, p.Config, "[update]\ncheck = \"no\"\nauto = 1\nchannel = \"beta\"\n")
	c, warns, err = Load(p.Config)
	if err != nil || !c.Update.Check || c.Update.Auto {
		t.Fatalf("bad values: %+v %v", c.Update, err)
	}
	for _, w := range []string{"update.check must be true or false", "update.auto must be true or false", `unknown key "update.channel"`} {
		if !hasWarn(warns, w) {
			t.Errorf("missing warning %q in %v", w, warns)
		}
	}
	write(t, p.Config, "update = true\n")
	if _, warns, _ = Load(p.Config); !hasWarn(warns, "update must be a table") {
		t.Errorf("warnings %v", warns)
	}
}
