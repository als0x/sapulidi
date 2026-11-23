package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
	packsload "github.com/als0x/sapulidi/internal/packs"
	"github.com/als0x/sapulidi/packs"
)

func TestEmbeddedPacksAllValid(t *testing.T) {
	rules, errs := packsload.Load(packs.FS, "")
	for _, err := range errs {
		t.Errorf("pack error: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("no rules loaded from the embedded packs")
	}
	// Every pack file that exists must have produced at least one rule for some OS (a darwin-only pack loads
	// as empty on Linux, which is not an error); a pack whose rules all fail validation is dropped as a whole
	// and its errors are reported above.
	files, _ := fs.Glob(packs.FS, "*.toml")
	loaded := map[string]bool{}
	for _, goos := range []string{"darwin", "linux"} {
		rs, _ := packsload.LoadFor(packs.FS, "", goos)
		for _, r := range rs {
			loaded[r.Pack] = true
		}
	}
	for _, f := range files {
		name := strings.TrimSuffix(f, ".toml")
		if !loaded[name] {
			t.Errorf("pack %s loaded no rule", name)
		}
	}
}

// ruleHash is a hash of everything that decides what an auto rule matches: its pack-visible content without
// the pack name, id and revision.
func ruleHash(r core.Rule) string {
	s := fmt.Sprintf("%s|%s|%s|%q|%q|%s|%s|%s|%s|%s|%q",
		r.Category, r.Bucket, r.Kind, r.Names, r.Markers, r.MinAge, r.Tool, r.Action, r.Strategy, r.Location, r.OS)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// parseGolden reads "<rule key> <hash>" lines.
func parseGolden(data string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, h, _ := strings.Cut(line, " ")
		m[k] = strings.TrimSpace(h)
	}
	return m
}

// goldenProblems compares the current auto rules of one pack with its golden map (spec S2).
func goldenProblems(pack string, rules []core.Rule, golden map[string]string) []string {
	var out []string
	current := map[string]string{}
	for _, r := range rules {
		if r.Pack == pack && r.Bucket == core.Auto {
			current[r.Key()] = ruleHash(r)
		}
	}
	keys := make([]string, 0, len(current))
	for k := range current {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		want, ok := golden[k]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("auto rule %s is not in packs/testdata/autorules/%s.golden; add the line %q", k, pack, k+" "+current[k]))
		case want != current[k]:
			out = append(out, fmt.Sprintf("auto rule %s changed without bumping rev (new hash %s)", k, current[k]))
		}
	}
	for k := range golden {
		if _, ok := current[k]; !ok {
			out = append(out, fmt.Sprintf("golden line %s of pack %s has no auto rule: remove it or restore the rule", k, pack))
		}
	}
	return out
}

func TestAutoRulesGolden(t *testing.T) {
	rules, errs := packsload.Load(packs.FS, "")
	for _, err := range errs {
		t.Errorf("pack error: %v", err)
	}
	// Every OS-specific rule is filtered by Load for the running OS; the golden files list the rules of the
	// running OS only when a pack uses `os`, which no auto rule does today.
	files, _ := fs.Glob(packs.FS, "*.toml")
	for _, f := range files {
		pack := strings.TrimSuffix(f, ".toml")
		var golden map[string]string
		if data, err := fs.ReadFile(packs.FS, "testdata/autorules/"+pack+".golden"); err == nil {
			golden = parseGolden(string(data))
		}
		for _, p := range goldenProblems(pack, rules, golden) {
			t.Error(p)
		}
	}
}

func TestGoldenDetectsChangeWithoutRevBump(t *testing.T) {
	r := core.Rule{Pack: "x", ID: "a", Rev: 1, Category: "temp", Bucket: core.Auto, Kind: core.KindTemp,
		Names: []string{"abc*"}, MinAge: 3600e9}
	golden := map[string]string{r.Key(): ruleHash(r)}
	if p := goldenProblems("x", []core.Rule{r}, golden); len(p) != 0 {
		t.Fatalf("unchanged rule: %v", p)
	}
	widened := r
	widened.Names = []string{"abc*", "def*"}
	if p := goldenProblems("x", []core.Rule{widened}, golden); len(p) != 1 || !strings.Contains(p[0], "without bumping rev") {
		t.Fatalf("widened rule at the same rev: %v", p)
	}
	widened.Rev = 2
	if p := goldenProblems("x", []core.Rule{widened}, golden); len(p) != 2 {
		t.Fatalf("bumped rev but golden not updated: %v", p)
	}
}
