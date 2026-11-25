package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/als0x/sapulidi/internal/testutil"
)

var allEcoPacks = []string{"rust", "jvm", "c-cpp", "dart", "python"}

// oneCandidate scans build-output and requires exactly one candidate, at path, with the given rule key.
func oneCandidate(t *testing.T, f *fixture, path, key string) {
	t.Helper()
	cands, kept := scan(t, f, "build-output")
	if len(cands) != 1 || cands[0].Path != path || cands[0].RuleKey != key {
		t.Fatalf("want one candidate %s %s; got %+v, kept %v", path, key, cands, kept)
	}
	if len(kept) != 0 {
		t.Fatalf("a candidate path must not also be kept: %v", kept)
	}
	if err := cat(t, "build-output").Proof(f.env, cands[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
}

func TestSharedNameTargetPicksRuleWithMarker(t *testing.T) {
	for marker, key := range map[string]string{"Cargo.toml": "rust/target@2", "pom.xml": "jvm/maven-target@2"} {
		f := newFixture(t, allEcoPacks...)
		p := mkProject(t, f.root, "proj", true, marker, "target/out.bin")
		ageTree(t, p)
		oneCandidate(t, f, filepath.Join(p, "target"), key)
	}
}

func TestSharedNameBuildPicksRuleWithMarker(t *testing.T) {
	cases := map[string]string{
		"CMakeLists.txt": "c-cpp/cmake-build@1",
		"pubspec.yaml":   "dart/dart-build@2",
		"build.gradle":   "jvm/gradle-build@2",
		"pyproject.toml": "python/build@1",
	}
	for marker, key := range cases {
		f := newFixture(t, allEcoPacks...)
		p := mkProject(t, f.root, "proj", true, marker, "build/out.bin")
		ageTree(t, p)
		oneCandidate(t, f, filepath.Join(p, "build"), key)
	}
}

func TestSharedNameBuildWithoutMarkerIsKeptNoMarker(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "README.md", "build/out.bin")
	ageTree(t, p)
	cands, kept := scan(t, f, "build-output")
	b := filepath.Join(p, "build")
	if len(cands) != 0 || keptWhy(kept, b) != "no-marker" || len(kept) != 1 {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestSharedNameTwoMarkersOneCandidateFirstRule(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "Cargo.toml", "pom.xml", "target/out.bin")
	ageTree(t, p)
	// Pack order (packs sorted by file name) puts jvm before rust: jvm wins.
	oneCandidate(t, f, filepath.Join(p, "target"), "jvm/maven-target@2")
}

func TestSharedNameProofRefusesWhenWinningMarkerAloneGone(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "Cargo.toml", "pom.xml", "target/out.bin")
	ageTree(t, p)
	cands, _ := scan(t, f, "build-output")
	if len(cands) != 1 || cands[0].RuleKey != "jvm/maven-target@2" {
		t.Fatalf("candidates %+v", cands)
	}
	if err := os.Remove(filepath.Join(p, "pom.xml")); err != nil {
		t.Fatal(err)
	}
	if err := cat(t, "build-output").Proof(f.env, cands[0]); err == nil {
		t.Fatal("proof must refuse: the rule in RuleKey no longer proves the folder")
	}
}

func keptOne(t *testing.T, f *fixture, path, why, key string) {
	t.Helper()
	cands, kept := scan(t, f, "build-output")
	if len(cands) != 0 || len(kept) != 1 || kept[0].Path != path || kept[0].Why != why || kept[0].RuleKey != key {
		t.Fatalf("want one kept %s %s %s; candidates %v, kept %+v", path, why, key, candPaths(cands), kept)
	}
}

func TestSharedNameMavenYoungAndTracked(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "young", true, "pom.xml", "target/out.bin")
	keptOne(t, f, filepath.Join(p, "target"), "young", "jvm/maven-target@2")

	f = newFixture(t, allEcoPacks...)
	p = mkProject(t, f.root, "tracked", true, "pom.xml", "target/out.bin", "target/hand.bin")
	git(t, p, "add", "-f", "target/hand.bin")
	git(t, p, "commit", "-m", "track")
	ageTree(t, p)
	keptOne(t, f, filepath.Join(p, "target"), "tracked", "jvm/maven-target@2")
}

func TestSharedNamePythonBuildYoung(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "pyproject.toml", "build/out.bin")
	keptOne(t, f, filepath.Join(p, "build"), "young", "python/build@1")
}

func TestSharedNameNestedGitKept(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "Cargo.toml", "target/out.bin")
	if err := os.MkdirAll(filepath.Join(p, "target", "inner", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	ageTree(t, p)
	cands, kept := scan(t, f, "build-output")
	if len(cands) != 0 || len(kept) != 1 || kept[0].Why == "no-marker" || kept[0].RuleKey != "rust/target@2" {
		t.Fatalf("candidates %v, kept %+v", candPaths(cands), kept)
	}
}

func TestSharedNameDisabledRules(t *testing.T) {
	// First rule (jvm) disabled: the enabled rust rule carries the candidate.
	f := newFixture(t, allEcoPacks...)
	f.env.Settings.(*testutil.FakeSettings).DisabledRules = map[string]bool{"jvm/maven-target@2": true}
	p := mkProject(t, f.root, "both", true, "Cargo.toml", "pom.xml", "target/out.bin")
	ageTree(t, p)
	oneCandidate(t, f, filepath.Join(p, "target"), "rust/target@2")

	// Both disabled: as before, the first passing rule is proposed (the engine drops it).
	f = newFixture(t, allEcoPacks...)
	f.env.Settings.(*testutil.FakeSettings).DisabledRules = map[string]bool{"jvm/maven-target@2": true, "rust/target@2": true}
	p = mkProject(t, f.root, "both", true, "Cargo.toml", "pom.xml", "target/out.bin")
	ageTree(t, p)
	cands, kept := scan(t, f, "build-output")
	if len(cands) != 1 || cands[0].RuleKey != "jvm/maven-target@2" || len(kept) != 0 {
		t.Fatalf("candidates %+v, kept %v", cands, kept)
	}

	// A single disabled rule: unchanged.
	f = newFixture(t, allEcoPacks...)
	f.env.Settings.(*testutil.FakeSettings).DisabledRules = map[string]bool{"rust/target@2": true}
	p = mkProject(t, f.root, "one", true, "Cargo.toml", "target/out.bin")
	ageTree(t, p)
	cands, kept = scan(t, f, "build-output")
	if len(cands) != 1 || cands[0].RuleKey != "rust/target@2" || len(kept) != 0 {
		t.Fatalf("candidates %+v, kept %v", cands, kept)
	}
}

func TestSharedNameYoungKeepsYoungNotNoMarker(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "Cargo.toml", "target/out.bin")
	cands, kept := scan(t, f, "build-output")
	tg := filepath.Join(p, "target")
	if len(cands) != 0 || keptWhy(kept, tg) != "young" || len(kept) != 1 || kept[0].RuleKey != "rust/target@2" {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestSharedNameTrackedKeepsTracked(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "Cargo.toml", "target/out.bin", "target/hand-made.bin")
	git(t, p, "add", "-f", "target/hand-made.bin")
	git(t, p, "commit", "-m", "track")
	ageTree(t, p)
	cands, kept := scan(t, f, "build-output")
	tg := filepath.Join(p, "target")
	if len(cands) != 0 || keptWhy(kept, tg) != "tracked" || len(kept) != 1 || kept[0].RuleKey != "rust/target@2" {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestSharedNameTargetWithoutMarkerKeptNoMarker(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "README.md", "target/out.bin")
	ageTree(t, p)
	cands, kept := scan(t, f, "build-output")
	tg := filepath.Join(p, "target")
	if len(cands) != 0 || keptWhy(kept, tg) != "no-marker" || len(kept) != 1 {
		t.Fatalf("candidates %v, kept %v", candPaths(cands), kept)
	}
}

func TestSharedNameProofRefusesWhenMarkerGone(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "Cargo.toml", "target/out.bin")
	ageTree(t, p)
	cands, _ := scan(t, f, "build-output")
	if len(cands) != 1 || cands[0].RuleKey != "rust/target@2" {
		t.Fatalf("candidates %+v", cands)
	}
	if err := os.Remove(filepath.Join(p, "Cargo.toml")); err != nil {
		t.Fatal(err)
	}
	if err := cat(t, "build-output").Proof(f.env, cands[0]); err == nil {
		t.Fatal("proof must refuse once the marker is gone")
	}
}
