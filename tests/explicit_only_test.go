package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const enginePkgSuffix = "/internal/engine"

// explicitViolations parses every Go file (tests included) under root/internal and root/cmd, except internal/engine
// and internal/tui, and reports each use of Explicit on engine.Options: a keyed literal field, a selector, a
// positional engine.Options literal, a dot import of the engine, a reflect FieldByName("Explicit") and any string
// literal "Explicit" (a decoder key). Explicit is an in-process option; the terminal UI is its only caller.
func explicitViolations(t testing.TB, root string) []string {
	t.Helper()
	var bad []string
	for _, top := range []string{"internal", "cmd"} {
		base := filepath.Join(root, top)
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/engine/") || strings.HasPrefix(rel, "internal/tui/") {
				return nil
			}
			file, perr := parser.ParseFile(token.NewFileSet(), p, nil, 0)
			if perr != nil {
				bad = append(bad, rel+": does not parse: "+perr.Error())
				return nil
			}
			for _, why := range explicitUses(file) {
				bad = append(bad, rel+": "+why)
			}
			return nil
		})
	}
	return bad
}

func explicitUses(file *ast.File) []string {
	var out []string
	engineNames := map[string]bool{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if !strings.HasSuffix("/"+path, enginePkgSuffix) {
			continue
		}
		switch {
		case imp.Name == nil:
			engineNames["engine"] = true
		case imp.Name.Name == ".":
			out = append(out, "dot import of the engine")
		default:
			engineNames[imp.Name.Name] = true
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.KeyValueExpr:
			if id, ok := x.Key.(*ast.Ident); ok && id.Name == "Explicit" {
				out = append(out, "keyed literal field Explicit")
			}
		case *ast.SelectorExpr:
			if x.Sel.Name == "Explicit" {
				out = append(out, "selector .Explicit")
			}
		case *ast.CompositeLit:
			sel, ok := x.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Options" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && engineNames[id.Name] {
				for _, e := range x.Elts {
					if _, keyed := e.(*ast.KeyValueExpr); !keyed {
						out = append(out, "positional engine.Options literal")
						break
					}
				}
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				if s, err := strconv.Unquote(x.Value); err == nil && s == "Explicit" {
					out = append(out, `string literal "Explicit" (reflect or decoder key)`)
				}
			}
		}
		return true
	})
	return out
}

func plantFile(t testing.TB, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitIsSetOnlyByEngineAndTUI(t *testing.T) {
	if bad := explicitViolations(t, repoRoot()); len(bad) > 0 {
		t.Fatalf("engine.Options.Explicit is used outside internal/engine and internal/tui:\n%s", strings.Join(bad, "\n"))
	}
}

func TestExplicitScanFlagsPlantedFiles(t *testing.T) {
	plant := map[string]string{
		"internal/engine/ok.go":  "package engine\nvar o = Options{Explicit: nil}\n",
		"internal/tui/ok.go":     "package tui\nfunc f() { o.Explicit = nil }\n",
		"internal/cli/keyed.go":  "package cli\nimport \"x/internal/engine\"\nvar o = engine.Options{Explicit: []string{\"/x\"}}\n",
		"internal/cli/sel.go":    "package cli\nfunc f() { o.Explicit = nil }\n",
		"internal/cli/x_test.go": "package cli\nimport e \"x/internal/engine\"\nvar o = e.Options{Explicit: nil}\n",
		"cmd/sapulidi/keyed.go":  "package main\nimport \"x/internal/engine\"\nvar o = engine.Options{Explicit: []string{\"/x\"}}\n",
		"cmd/sapulidi/pos.go": "package main\nimport \"x/internal/engine\"\n" +
			"var o = engine.Options{false, false, 0, \"\", nil, nil, false, false, nil}\n",
		"cmd/sapulidi/refl.go": "package main\nimport \"reflect\"\nfunc f(v any) { reflect.ValueOf(v).FieldByName(\"Explicit\") }\n",
		"cmd/sapulidi/dot.go":  "package main\nimport . \"x/internal/engine\"\nvar _ = Options{}\n",
		"cmd/sapulidi/ok.go":   "package main\nimport \"x/internal/engine\"\nvar o = engine.Options{DryRun: true}\n",
	}
	root := t.TempDir()
	for name, src := range plant {
		plantFile(t, filepath.Join(root, filepath.FromSlash(name)), src)
	}
	got := map[string]bool{}
	for _, v := range explicitViolations(t, root) {
		got[strings.SplitN(v, ":", 2)[0]] = true
	}
	want := []string{"internal/cli/keyed.go", "internal/cli/sel.go", "internal/cli/x_test.go", "cmd/sapulidi/keyed.go",
		"cmd/sapulidi/pos.go", "cmd/sapulidi/refl.go", "cmd/sapulidi/dot.go"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("%s not flagged", w)
		}
	}
	for _, ok := range []string{"internal/engine/ok.go", "internal/tui/ok.go", "cmd/sapulidi/ok.go"} {
		if got[ok] {
			t.Errorf("%s flagged", ok)
		}
	}
}
