package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTrustKernelCannotImportCommerceOrAdmission(t *testing.T) {
	dir := filepath.Join(repositoryRoot(t), "internal", "trust")
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, forbidden := range []string{"safepatch/internal/domain/edition", "safepatch/internal/domain/feature", "safepatch/internal/domain/billing", "safepatch/internal/domain/entitlement", "safepatch/internal/entitlement", "safepatch/internal/authorization", "safepatch/internal/config", "safepatch/internal/licensing", "safepatch/internal/platform", "safepatch/internal/application", "safepatch/internal/agent"} {
				if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
					t.Errorf("TrustKernel depends on admission/commerce: %s", path)
				}
			}
		}
	}
}
