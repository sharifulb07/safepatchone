// Package architecture enforces source dependency boundaries without loading
// infrastructure SDKs or relying on the host platform's selected build files.
package architecture

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "safepatch"

// Every internal package tree must be explicitly classified. This prevents a
// new, unreviewed helper package from becoming an infrastructure escape hatch.
var corePackages = []string{
	"domain", "tenant", "entitlement", "authorization", "verification",
	"evidence", "intent", "spec", "sir", "graph", "proof", "policy", "risk",
	"trust", "provenance", "agent", "replay", "runtime", "audit",
}

var adapterPackages = []string{"billing", "licensing", "config", "platform"}

// An allowlist also excludes newly introduced SDKs, not just known providers.
// Standard-library infrastructure (os, os/exec, net/http, database/sql, plugin,
// unsafe, cgo, etc.) is intentionally absent. This is a dependency rule, not
// a claim that every operation inside an allowed package is pure or secure.
var allowedStandardImports = []string{
	"bytes", "cmp", "context", "crypto", "crypto/ed25519", "crypto/sha256",
	"crypto/sha512", "crypto/subtle", "encoding", "encoding/base64",
	"encoding/binary", "encoding/hex", "encoding/json", "errors", "fmt",
	"hash", "hash/crc32", "io", "io/fs", "iter", "maps", "math", "math/big",
	"math/bits", "net/netip", "net/url", "path", "path/filepath", "regexp",
	"slices", "sort", "strconv", "strings", "sync", "sync/atomic", "time",
	"unicode", "unicode/utf8", "unicode/utf16",
}

func TestRepositoryArchitecture(t *testing.T) {
	root := repositoryRoot(t)
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(module))
	if len(fields) < 2 || fields[0] != "module" || fields[1] != modulePath {
		t.Fatal("root module identity changed; update the architecture policy explicitly")
	}
	violations, err := inspectSource(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}

	// Keep the requested boundaries visible even before their first use case.
	for _, dir := range append(append(slices.Clone(corePackages), adapterPackages...), "application") {
		if info, err := os.Stat(filepath.Join(root, "internal", dir)); err != nil || !info.IsDir() {
			t.Errorf("missing internal boundary: %s", dir)
		}
	}
	for _, dir := range []string{
		"cmd/safepatch", "cmd/safepatch-api", "cmd/safepatch-worker",
		"cmd/safepatch-collector", "cmd/safepatch-mcp-gateway", "cmd/safepatch-replay",
		"languages", "frameworks", "integrations", "migrations", "api/openapi", "deploy", "web", "docs",
	} {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil || !info.IsDir() {
			t.Errorf("missing repository boundary: %s", dir)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("cannot locate repository go.mod")
		}
		dir = parent
	}
}

func inspectSource(source fs.FS) ([]string, error) {
	var violations []string
	err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", name, walkErr)
		}
		if entry.IsDir() && slices.Contains([]string{".git", "node_modules", "vendor", "web/node_modules", "web/.next"}, name) {
			return fs.SkipDir
		}
		// A link could hide files from WalkDir. Reject it instead of claiming that
		// its target has been inspected. No linked paths are traversed here.
		if entry.Type()&fs.ModeSymlink != 0 {
			violations = append(violations, name+": symlink is outside the source audit policy")
			return nil
		}
		if entry.IsDir() {
			if slices.Contains([]string{"community", "developer", "team", "business", "enterprise"}, strings.ToLower(entry.Name())) {
				violations = append(violations, name+": edition directories are forbidden")
			}
			return nil
		}
		if entry.Name() == "go.mod" && name != "go.mod" {
			violations = append(violations, name+": nested Go modules are forbidden; use the shared module")
		}
		if entry.Name() == "go.work" {
			violations = append(violations, name+": Go workspaces cannot replace the shared module boundary")
		}
		if path.Ext(name) != ".go" {
			return nil
		}
		parts := strings.Split(name, "/")
		if parts[0] != "internal" {
			return nil
		}
		if len(parts) < 3 || (!slices.Contains(corePackages, parts[1]) && !slices.Contains(adapterPackages, parts[1]) && parts[1] != "application") {
			violations = append(violations, name+": unclassified internal package")
			return nil
		}
		if slices.Contains(adapterPackages, parts[1]) {
			return nil
		}
		content, err := fs.ReadFile(source, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, name, content, parser.AllErrors)
		if err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}
		// Do not use build.Import or go list here: they omit files for other
		// operating systems, architectures, build tags, and sometimes tests.
		for _, imported := range file.Imports {
			dependency, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return fmt.Errorf("import in %s: %w", name, err)
			}
			if !allowedImport(parts[1], dependency, strings.HasSuffix(name, "_test.go")) {
				violations = append(violations, fmt.Sprintf("%s: forbidden import %q; depend on core contracts and inject adapters", positions.Position(imported.Pos()), dependency))
			}
		}
		return nil
	})
	slices.Sort(violations)
	return violations, err
}

func allowedImport(owner, dependency string, testFile bool) bool {
	if slices.Contains(allowedStandardImports, dependency) {
		return true
	}
	if testFile && slices.Contains([]string{"testing", "testing/fstest", "testing/quick"}, dependency) {
		return true
	}
	if !strings.HasPrefix(dependency, modulePath+"/internal/") || path.Clean(dependency) != dependency {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(dependency, modulePath+"/internal/"), "/")
	target := parts[0]
	if (owner == "policy" || owner == "intent" || owner == "spec" || owner == "sir" || owner == "verification" || owner == "graph" || owner == "proof") && (target == "entitlement" || target == "authorization" || target == "domain" && len(parts) > 1 && slices.Contains([]string{"edition", "entitlement", "feature", "billing"}, parts[1])) {
		return false // Shared semantics cannot depend on commercial admission.
	}
	if owner == "authorization" && target == "domain" && len(parts) > 1 && slices.Contains([]string{"entitlement", "edition", "billing"}, parts[1]) {
		return false // Commercial metadata must not determine actor permissions.
	}
	if owner == "domain" {
		return target == "domain"
	}
	if owner == "tenant" {
		return target == "tenant" || target == "domain"
	}
	if owner == "entitlement" || owner == "authorization" {
		return target == owner || target == "tenant" || target == "domain"
	}
	if owner == "application" && target == "application" {
		return true
	}
	return slices.Contains(corePackages, target)
}
