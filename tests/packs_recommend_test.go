package tests

import (
	"io/fs"
	"slices"
	"strconv"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/als0x/sapulidi/packs"
)

type packRule struct {
	ID        string `toml:"id"`
	Rev       int    `toml:"rev"`
	Bucket    string `toml:"bucket"`
	Recommend bool   `toml:"recommend"`
}

// embeddedRules reads every built-in pack as plain TOML, so rules of both operating systems are seen and no
// category has to be registered.
func embeddedRules(t *testing.T) (recommended, auto []string) {
	t.Helper()
	files, err := fs.Glob(packs.FS, "*.toml")
	if err != nil || len(files) == 0 {
		t.Fatalf("packs %v err %v", files, err)
	}
	for _, f := range files {
		var p struct {
			Pack  string     `toml:"pack"`
			Rules []packRule `toml:"rule"`
		}
		if _, err := toml.DecodeFS(packs.FS, f, &p); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, r := range p.Rules {
			key := p.Pack + "/" + r.ID + "@" + strconv.Itoa(r.Rev)
			if r.Recommend {
				recommended = append(recommended, key)
			}
			if r.Bucket == "auto" {
				auto = append(auto, key)
			}
		}
	}
	slices.Sort(recommended)
	return recommended, auto
}

// Changing which rules are recommended is the owner's decision, and changing a rule's flag bumps its rev so
// that consent given to the old rev does not carry over.
func TestRecommendedRulesPinned(t *testing.T) {
	want := []string{
		"agent/claude-plugin-cache@2",
		"agent/claude-versions@2",
		"apple/derived-data@2",
		"code-index/code-index@2",
		"crash/dumps@2",
		"dart/dart-build@2",
		"elixir/mix-build@2",
		"jvm/gradle-build@2",
		"jvm/hs-err@2",
		"jvm/maven-target@2",
		"node/next@2",
		"node/node-modules@2",
		"node/nuxt@2",
		"node/parcel-cache@2",
		"node/svelte-kit@2",
		"node/turbo@2",
		"python/egg-info@2",
		"python/tox-nox@2",
		"rust/target@2",
		"swift/swiftpm-build@2",
		"worktrees/linked@2",
		"zig/zig-build@2",
	}
	got, _ := embeddedRules(t)
	if !slices.Equal(got, want) {
		t.Errorf("recommended rules:\n got %q\nwant %q", got, want)
	}
}

// Consent for the auto rules of 0.2.0 must survive the upgrade.
func TestAutoRuleRevsUnchangedSinceV020(t *testing.T) {
	v020 := []string{
		"agent/claude-scratch@1",
		"agent/codex-tmp@1",
		"python/hypothesis@1",
		"python/mypy-cache@1",
		"python/pycache@1",
		"python/pytest-cache@1",
		"python/ruff-cache@1",
		"temp/bunx@1",
		"temp/go-build@1",
		"temp/jest@1",
		"temp/node-compile-cache@1",
		"temp/pip@1",
		"temp/playwright@1",
		"temp/pytest@1",
		"temp/tsx@1",
		"temp/vitest@1",
		"test-artefacts/test-output@1",
		"worktrees/prune@1",
	}
	_, auto := embeddedRules(t)
	for _, key := range v020 {
		if !slices.Contains(auto, key) {
			t.Errorf("auto rule %s of v0.2.0 is missing or changed its rev", key)
		}
	}
}
