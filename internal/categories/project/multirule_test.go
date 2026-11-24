package project

import (
	"os"
	"path/filepath"
	"testing"
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
	for marker, key := range map[string]string{"Cargo.toml": "rust/target@1", "pom.xml": "jvm/maven-target@1"} {
		f := newFixture(t, allEcoPacks...)
		p := mkProject(t, f.root, "proj", true, marker, "target/out.bin")
		ageTree(t, p)
		oneCandidate(t, f, filepath.Join(p, "target"), key)
	}
}

func TestSharedNameBuildPicksRuleWithMarker(t *testing.T) {
	cases := map[string]string{
		"CMakeLists.txt": "c-cpp/cmake-build@1",
		"pubspec.yaml":   "dart/dart-build@1",
		"build.gradle":   "jvm/gradle-build@1",
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
	want := ""
	for _, r := range f.env.Rules {
		if r.Key() == "rust/target@1" || r.Key() == "jvm/maven-target@1" {
			want = r.Key()
			break
		}
	}
	oneCandidate(t, f, filepath.Join(p, "target"), want)
}

func TestSharedNameYoungKeepsYoungNotNoMarker(t *testing.T) {
	f := newFixture(t, allEcoPacks...)
	p := mkProject(t, f.root, "proj", true, "Cargo.toml", "target/out.bin")
	cands, kept := scan(t, f, "build-output")
	tg := filepath.Join(p, "target")
	if len(cands) != 0 || keptWhy(kept, tg) != "young" || len(kept) != 1 {
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
	if len(cands) != 0 || keptWhy(kept, tg) != "tracked" || len(kept) != 1 {
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
	if len(cands) != 1 || cands[0].RuleKey != "rust/target@1" {
		t.Fatalf("candidates %+v", cands)
	}
	if err := os.Remove(filepath.Join(p, "Cargo.toml")); err != nil {
		t.Fatal(err)
	}
	if err := cat(t, "build-output").Proof(f.env, cands[0]); err == nil {
		t.Fatal("proof must refuse once the marker is gone")
	}
}
