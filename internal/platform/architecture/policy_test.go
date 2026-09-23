package architecture

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestImportBoundaryFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, owner, dependency string
		allow                   bool
	}{
		{"domain errors", "domain", "errors", true},
		{"domain nested types", "domain", "safepatch/internal/domain/project", true},
		{"shared domain", "verification", "safepatch/internal/domain", true},
		{"shared application contracts", "application", "safepatch/internal/entitlement", true},
		{"actor ownership", "authorization", "safepatch/internal/tenant", true},
		{"billing", "domain", "safepatch/internal/billing", false},
		{"billing port", "application", "safepatch/internal/domain/billing", true},
		{"Stripe adapter in domain", "domain", "safepatch/internal/billing/stripe", false},
		{"Stripe adapter in application", "application", "safepatch/internal/billing/stripe", false},
		{"billing cannot authorize", "authorization", "safepatch/internal/domain/billing", false},
		{"billing cannot change verification", "verification", "safepatch/internal/domain/billing", false},
		{"Stripe", "proof", "github.com/stripe/stripe-go/v82", false},
		{"PostgreSQL", "graph", "github.com/jackc/pgx/v5", false},
		{"PostgreSQL legacy", "evidence", "github.com/lib/pq", false},
		{"NATS", "verification", "github.com/nats-io/nats.go", false},
		{"GitHub", "intent", "github.com/google/go-github/v74/github", false},
		{"unknown SDK", "domain", "example.org/new/storage", false},
		{"SQL stdlib", "domain", "database/sql", false},
		{"process execution", "replay", "os/exec", false},
		{"host filesystem", "evidence", "os", false},
		{"HTTP stdlib", "agent", "net/http", false},
		{"raw sockets", "runtime", "net", false},
		{"cgo", "domain", "C", false},
		{"unsafe", "domain", "unsafe", false},
		{"dynamic plugin", "domain", "plugin", false},
		{"platform adapter", "policy", "safepatch/internal/platform/postgres", false},
		{"policy entitlement service", "policy", "safepatch/internal/entitlement", false},
		{"policy edition metadata", "policy", "safepatch/internal/domain/edition", false},
		{"policy feature catalog", "policy", "safepatch/internal/domain/feature", false},
		{"policy limits", "policy", "safepatch/internal/domain/entitlement", false},
		{"policy actor permission", "policy", "safepatch/internal/authorization", false},
		{"policy evidence", "policy", "safepatch/internal/evidence", true},
		{"intent entitlement service", "intent", "safepatch/internal/entitlement", false},
		{"intent edition metadata", "intent", "safepatch/internal/domain/edition", false},
		{"intent feature catalog", "intent", "safepatch/internal/domain/feature", false},
		{"intent resource limits", "intent", "safepatch/internal/domain/entitlement", false},
		{"intent actor permission", "intent", "safepatch/internal/authorization", false},
		{"intent shared evidence", "intent", "safepatch/internal/evidence", true},
		{"spec entitlement service", "spec", "safepatch/internal/entitlement", false},
		{"spec edition metadata", "spec", "safepatch/internal/domain/edition", false},
		{"spec feature catalog", "spec", "safepatch/internal/domain/feature", false},
		{"spec resource limits", "spec", "safepatch/internal/domain/entitlement", false},
		{"spec actor permission", "spec", "safepatch/internal/authorization", false},
		{"spec shared intent", "spec", "safepatch/internal/intent", true},
		{"sir entitlement service", "sir", "safepatch/internal/entitlement", false},
		{"sir edition metadata", "sir", "safepatch/internal/domain/edition", false},
		{"sir feature catalog", "sir", "safepatch/internal/domain/feature", false},
		{"sir resource limits", "sir", "safepatch/internal/domain/entitlement", false},
		{"sir actor permission", "sir", "safepatch/internal/authorization", false},
		{"sir shared evidence", "sir", "safepatch/internal/evidence", true},
		{"graph shared evidence", "graph", "safepatch/internal/evidence", true},
		{"proof shared verifier", "proof", "safepatch/internal/verification", true},
		{"proof edition metadata", "proof", "safepatch/internal/domain/edition", false},
		{"proof feature catalog", "proof", "safepatch/internal/domain/feature", false},
		{"proof resource limits", "proof", "safepatch/internal/domain/entitlement", false},
		{"proof entitlement service", "proof", "safepatch/internal/entitlement", false},
		{"proof actor permission", "proof", "safepatch/internal/authorization", false},
		{"graph edition metadata", "graph", "safepatch/internal/domain/edition", false},
		{"graph feature catalog", "graph", "safepatch/internal/domain/feature", false},
		{"graph resource limits", "graph", "safepatch/internal/domain/entitlement", false},
		{"graph entitlement service", "graph", "safepatch/internal/entitlement", false},
		{"graph actor permission", "graph", "safepatch/internal/authorization", false},
		{"language SDK feature catalog", "verification", "safepatch/internal/domain/feature", false},
		{"language SDK edition metadata", "verification", "safepatch/internal/domain/edition", false},
		{"language SDK entitlement service", "verification", "safepatch/internal/entitlement", false},
		{"language SDK actor permission", "verification", "safepatch/internal/authorization", false},
		{"licensing adapter", "entitlement", "safepatch/internal/licensing", false},
		{"subscription versus permission", "authorization", "safepatch/internal/entitlement", false},
		{"permission cannot use entitlement records", "authorization", "safepatch/internal/domain/entitlement", false},
		{"permission cannot use edition metadata", "authorization", "safepatch/internal/domain/edition", false},
		{"permission versus subscription", "entitlement", "safepatch/internal/authorization", false},
		{"application billing", "application", "safepatch/internal/billing", false},
		{"application SDK", "application", "github.com/nats-io/nats.go", false},
		{"domain direction", "domain", "safepatch/internal/application", false},
		{"core direction", "proof", "safepatch/internal/application", false},
		{"tenant direction", "tenant", "safepatch/internal/graph", false},
		{"domain lookalike", "domain", "safepatch/internal/domainextra", false},
		{"unclassified helper", "graph", "safepatch/internal/helper", false},
		{"external integration", "graph", "safepatch/integrations/github", false},
		{"relative path", "domain", "../billing", false},
		{"unclean module path", "domain", "safepatch/internal/domain/../billing", false},
		{"test harness in production", "domain", "testing", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := fstest.MapFS{
				"internal/" + tc.owner + "/example.go": {Data: []byte("package fixture\nimport alias \"" + tc.dependency + "\"\n")},
			}
			violations, err := inspectSource(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if tc.allow && len(violations) != 0 {
				t.Fatalf("allowed import rejected: %v", violations)
			}
			if !tc.allow && (len(violations) != 1 || !strings.Contains(violations[0], tc.dependency)) {
				t.Fatalf("expected import violation for %s: %v", tc.dependency, violations)
			}
		})
	}
}

func TestAllCorePackagesAndApplicationAreProtected(t *testing.T) {
	for _, owner := range append(append([]string{}, corePackages...), "application") {
		if allowedImport(owner, "safepatch/internal/platform", false) || allowedImport(owner, "example.org/infra", false) {
			t.Errorf("%s permits infrastructure", owner)
		}
	}
}

func TestBuildTagsGeneratedCodeAndTestFilesCannotHideImports(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"internal/domain/nested/tagged_plan9.go", "//go:build never_enabled\n\npackage nested\nimport _ \"database/sql\"\n"},
		{"internal/proof/generated.go", "// Code generated by fixture. DO NOT EDIT.\npackage proof\nimport . \"net/http\"\n"},
		{"internal/tenant/access_test.go", "package tenant_test\nimport alias \"safepatch/internal/billing\"\n"},
		{"internal/verification/_ignored.go", "package verification\nimport \"os/exec\"\n"},
		{"internal/domain/node_modules/helper.go", "package helper\nimport \"os\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			violations, err := inspectSource(fstest.MapFS{tc.name: {Data: []byte(tc.content)}})
			if err != nil || len(violations) != 1 {
				t.Fatalf("expected one violation, got %v, %v", violations, err)
			}
		})
	}
}

func TestIndirectInfrastructureCannotHideInCoreHelper(t *testing.T) {
	violations, err := inspectSource(fstest.MapFS{
		"internal/application/usecase.go": {Data: []byte("package application\nimport \"safepatch/internal/graph/helper\"\n")},
		"internal/graph/helper/store.go":  {Data: []byte("package helper\nimport \"github.com/jackc/pgx/v5\"\n")},
	})
	if err != nil || len(violations) != 1 || !strings.Contains(violations[0], "pgx") {
		t.Fatalf("expected helper infrastructure violation, got %v, %v", violations, err)
	}
}

func TestAdaptersAndPureTestsAreAllowed(t *testing.T) {
	violations, err := inspectSource(fstest.MapFS{
		"internal/platform/postgres/store.go": {Data: []byte("package postgres\nimport \"github.com/jackc/pgx/v5\"\n")},
		"internal/billing/provider.go":        {Data: []byte("package billing\nimport \"github.com/stripe/stripe-go/v82\"\n")},
		"internal/domain/value_test.go":       {Data: []byte("package domain_test\nimport \"testing\"\n")},
	})
	if err != nil || len(violations) != 0 {
		t.Fatalf("valid boundaries rejected: %v, %v", violations, err)
	}
}

func TestRepositoryStructureViolations(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"internal/helper/doc.go", "unclassified internal package"},
		{"internal/domain/enterprise/doc.go", "edition directories"},
		{"cmd/Enterprise/main.go", "edition directories"},
		{"integrations/github/go.mod", "nested Go modules"},
		{"go.work", "Go workspaces"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			violations, err := inspectSource(fstest.MapFS{tc.name: {Data: []byte("package fixture\n")}})
			if err != nil || len(violations) != 1 || !strings.Contains(violations[0], tc.want) {
				t.Fatalf("expected %q, got %v, %v", tc.want, violations, err)
			}
		})
	}
}

func TestInspectionErrorsFailClosed(t *testing.T) {
	t.Run("malformed source", func(t *testing.T) {
		_, err := inspectSource(fstest.MapFS{"internal/domain/broken.go": {Data: []byte("package domain\nfunc broken(")}})
		if err == nil || !strings.Contains(err.Error(), "parse internal/domain/broken.go") {
			t.Fatalf("expected parse failure, got %v", err)
		}
	})
	t.Run("unreadable filesystem", func(t *testing.T) {
		if _, err := inspectSource(unreadableFS{}); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("expected permission failure, got %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		violations, err := inspectSource(fstest.MapFS{"internal/domain/linked.go": {Mode: fs.ModeSymlink, Data: []byte("outside")}})
		if err != nil || len(violations) != 1 || !strings.Contains(violations[0], "symlink") {
			t.Fatalf("expected rejected symlink, got %v, %v", violations, err)
		}
	})
}

type unreadableFS struct{}

func (unreadableFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }
