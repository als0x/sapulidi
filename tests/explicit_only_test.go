package tests

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var explicitSet = regexp.MustCompile(`\bExplicit:|\.Explicit\s*=[^=]`)

// explicitViolations lists the non-test Go files under root/internal, other than internal/engine and internal/tui,
// that set engine.Options.Explicit. Explicit is an in-process option with the TUI as its only caller.
func explicitViolations(root string) []string {
	var bad []string
	base := filepath.Join(root, "internal")
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "engine/") || strings.HasPrefix(rel, "tui/") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && explicitSet.Match(b) {
			bad = append(bad, "internal/"+rel)
		}
		return nil
	})
	return bad
}

func TestExplicitIsSetOnlyByEngineAndTUI(t *testing.T) {
	if bad := explicitViolations(repoRoot()); len(bad) > 0 {
		t.Fatalf("engine.Options.Explicit is set outside internal/engine and internal/tui: %v", bad)
	}
}

func TestExplicitScanFlagsPlantedFile(t *testing.T) {
	root := t.TempDir()
	for name, src := range map[string]string{
		"internal/engine/ok.go":  "package engine\nvar o = Options{Explicit: nil}\n",
		"internal/tui/ok.go":     "package tui\nfunc f() { o.Explicit = nil }\n",
		"internal/cli/plant.go":  "package cli\nvar o = engine.Options{Explicit: []string{\"/x\"}}\n",
		"internal/cli/plant2.go": "package cli\nfunc f() { o.Explicit = nil }\n",
		"internal/cli/clean.go":  "package cli\nfunc g() { _ = o.Explicit == nil }\n",
		"internal/cli/x_test.go": "package cli\nvar o = engine.Options{Explicit: nil}\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(explicitViolations(root), ",")
	if got != "internal/cli/plant.go,internal/cli/plant2.go" {
		t.Fatalf("got %q", got)
	}
}
