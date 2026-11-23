package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Packages allowed to delete: the engine, test helpers, and packages that remove only
// sapulidi's own files (plan "Global Constraints").
var removeAllowed = []string{
	"internal/engine/", "internal/testutil/", "internal/state/",
	"internal/schedule/", "internal/agent/", "internal/delivery/", "internal/install/",
}

// forbidden maps an import path to the selectors that delete files.
var forbidden = map[string][]string{
	"os":      {"Remove", "RemoveAll"},
	"syscall": {"Unlink", "Rmdir", "Unlinkat"},
}

// scanRemoves parses every non-test .go file outside the allowed packages and reports each
// reference (call or function value) to a deleting function, resolving import aliases, and
// exec.Command("rm", ...). Read or parse errors are returned as findings too.
func scanRemoves(root string, files []string) []string {
	var found []string
	fset := token.NewFileSet()
	for _, rel := range files {
		slash := filepath.ToSlash(rel)
		if !strings.HasSuffix(slash, ".go") || strings.HasSuffix(slash, "_test.go") ||
			strings.HasPrefix(slash, "docs/") || strings.HasPrefix(slash, ".worktrees/") {
			continue
		}
		skip := false
		for _, p := range removeAllowed {
			if strings.HasPrefix(slash, p) {
				skip = true
			}
		}
		if skip {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			found = append(found, slash+": read error: "+err.Error())
			continue
		}
		f, err := parser.ParseFile(fset, slash, src, 0)
		if err != nil {
			found = append(found, slash+": parse error: "+err.Error())
			continue
		}
		names := map[string]string{} // local name → import path
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			local := filepath.Base(path)
			if imp.Name != nil {
				local = imp.Name.Name
			}
			names[local] = path
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				id, ok := x.X.(*ast.Ident)
				if !ok {
					break
				}
				for _, bad := range forbidden[names[id.Name]] {
					if x.Sel.Name == bad {
						found = append(found, slash+":"+strconv.Itoa(fset.Position(x.Pos()).Line)+": "+names[id.Name]+"."+bad)
					}
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok || len(x.Args) == 0 || sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
					break
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || names[id.Name] != "os/exec" {
					break
				}
				for _, a := range x.Args {
					if lit, ok := a.(*ast.BasicLit); ok && (lit.Value == `"rm"` || strings.HasSuffix(lit.Value, `/rm"`)) {
						found = append(found, slash+":"+strconv.Itoa(fset.Position(x.Pos()).Line)+": exec rm")
					}
				}
			}
			return true
		})
	}
	return found
}

func TestNoRemoveOutsideEngine(t *testing.T) {
	root := repoRoot()
	for _, f := range scanRemoves(root, listFiles(root)) {
		t.Errorf("deletion outside the engine: %s", f)
	}
}

func TestNoRemoveScannerFindsPlantedCall(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/bad/x.go", "package bad\nimport \"os\"\nfunc f() { os.RemoveAll(\"x\") }\n")
	write("internal/bad2/x.go", "package bad\nimport \"os\"\nfunc f() { os.Remove(\"x\") }\n")
	write("internal/alias/x.go", "package a\nimport o \"os\"\nfunc f() { o.RemoveAll(\"x\") }\n")
	write("internal/fv/x.go", "package a\nimport \"os\"\nfunc f() { rm := os.RemoveAll; rm(\"x\") }\n")
	write("internal/sys/x.go", "package a\nimport \"syscall\"\nfunc f() { syscall.Unlink(\"x\") }\n")
	write("internal/rm/x.go", "package a\nimport \"os/exec\"\nfunc f() { exec.Command(\"rm\", \"-rf\", \"x\") }\n")
	write("internal/broken/x.go", "package a\nfunc {\n")
	write("internal/engine/x.go", "package e\nimport \"os\"\nfunc f() { os.RemoveAll(\"x\") }\n")
	write("internal/bad/x_test.go", "package bad\nimport \"os\"\nfunc f() { os.RemoveAll(\"x\") }\n")
	write("internal/ok/x.go", "package ok\nimport \"os\"\nfunc f() { os.Stat(\"x\") }\n")
	files := []string{"internal/bad/x.go", "internal/bad2/x.go", "internal/alias/x.go", "internal/fv/x.go",
		"internal/sys/x.go", "internal/rm/x.go", "internal/broken/x.go", "internal/engine/x.go",
		"internal/bad/x_test.go", "internal/ok/x.go", "internal/missing/x.go"}
	got := strings.Join(scanRemoves(dir, files), "\n")
	for _, want := range []string{"internal/bad/x.go:3: os.RemoveAll", "internal/bad2/x.go:3: os.Remove",
		"internal/alias/x.go:3: os.RemoveAll", "internal/fv/x.go:3: os.RemoveAll", "internal/sys/x.go:3: syscall.Unlink",
		"internal/rm/x.go:3: exec rm", "internal/broken/x.go: parse error", "internal/missing/x.go: read error"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing finding %q in:\n%s", want, got)
		}
	}
	for _, not := range []string{"engine/", "x_test.go", "internal/ok/"} {
		if strings.Contains(got, not) {
			t.Errorf("unexpected finding for %q:\n%s", not, got)
		}
	}
}
