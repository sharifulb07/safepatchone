package tenant_test

import (
	"safepatch/internal/tenant"
	"strings"
	"testing"
)

func TestScopeRejectsMalformedIdentities(t *testing.T) {
	for _, id := range []string{"", "../org", "org/child", "org\x00", "org;delete", strings.Repeat("x", 129)} {
		if (tenant.Scope{OrganizationID: id, ResourceID: "proof_1"}).Validate() == nil || (tenant.Scope{OrganizationID: "org_a", ResourceID: id}).Validate() == nil {
			t.Fatal("malformed scope accepted")
		}
	}
	if err := (tenant.Scope{OrganizationID: "org_a", ResourceID: "proof_1"}).Validate(); err != nil {
		t.Fatal(err)
	}
}
