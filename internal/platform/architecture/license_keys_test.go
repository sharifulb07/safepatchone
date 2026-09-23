package architecture

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

func TestLicenseNodeContainsVerificationOnly(t *testing.T) {
	directory := filepath.Join(repositoryRoot(t), "internal", "licensing")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		aliases := map[string]bool{}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == "crypto/ed25519" {
				name := "ed25519"
				if imp.Name != nil {
					name = imp.Name.Name
				}
				if name == "." {
					t.Fatal("dot crypto import")
				}
				aliases[name] = true
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			selector, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := selector.X.(*ast.Ident)
			if !ok || !aliases[id.Name] {
				return true
			}
			switch selector.Sel.Name {
			case "PrivateKey", "Sign", "GenerateKey", "NewKeyFromSeed", "SeedSize":
				t.Errorf("license node uses signing API in %s: %s", entry.Name(), selector.Sel.Name)
			}
			return true
		})
	}
}
