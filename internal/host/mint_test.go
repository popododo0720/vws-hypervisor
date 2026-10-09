package host

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestDerivationsStayInMint(t *testing.T) {
	banned := map[string]bool{
		"processName": true,
		"tapName":     true,
		"guestMAC":    true,
		"bridgeName":  true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "mint.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && banned[id.Name] {
				t.Errorf("%s calls %s; derive names only from mint.go", name, id.Name)
			}
			return true
		})
	}
}
