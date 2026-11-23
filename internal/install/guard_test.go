package install

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// deletions lists the deleting calls in a file that are not an os.Remove inside removeRecorded.
// os.RemoveAll and syscall.Unlink are never allowed, even there.
func deletions(t *testing.T, file string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		local := p
		for i := len(p) - 1; i >= 0; i-- {
			if p[i] == '/' {
				local = p[i+1:]
				break
			}
		}
		if imp.Name != nil {
			local = imp.Name.Name
		}
		names[local] = p
	}
	var out []string
	var walk func(n ast.Node, inRemover bool)
	walk = func(n ast.Node, inRemover bool) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				if x != nil && x.Body != nil && x.Name.Name == "removeRecorded" && x.Recv == nil {
					walk(x.Body, true)
					return false
				}
			case *ast.SelectorExpr:
				id, ok := x.X.(*ast.Ident)
				if !ok {
					break
				}
				switch names[id.Name] + "." + x.Sel.Name {
				case "os.Remove":
					if !inRemover {
						out = append(out, "os.Remove outside removeRecorded at line "+strconv.Itoa(fset.Position(x.Pos()).Line))
					}
				case "os.RemoveAll", "syscall.Unlink", "syscall.Rmdir", "syscall.Unlinkat":
					out = append(out, names[id.Name]+"."+x.Sel.Name+" at line "+strconv.Itoa(fset.Position(x.Pos()).Line))
				}
			}
			return true
		})
	}
	walk(f, false)
	return out
}

func TestGuardFindsPlantedDeletes(t *testing.T) {
	// the scanner itself must flag a delete outside removeRecorded
	got := deletions(t, "testdata/plant.txt")
	if len(got) != 3 {
		t.Fatalf("scanner found %v", got)
	}
}
