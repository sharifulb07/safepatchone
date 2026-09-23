package mcpgateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"safepatch/internal/agent"
	"safepatch/internal/application"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/platform/agentstore"
	"safepatch/internal/platform/mcpgateway"
	"strings"
	"testing"
	"time"
)

type tenantFirstMCPAccess struct {
	*access
	t *testing.T
}

func (a tenantFirstMCPAccess) Has(ctx context.Context, org string, key feature.Key) (bool, error) {
	if org != "org" {
		a.t.Fatal("foreign tenant reached entitlement resolution")
	}
	return a.access.Has(ctx, org, key)
}

func TestMCPForeignLeaseAndScopeAcrossAllEditionPairs(t *testing.T) {
	for _, a := range edition.Editions() {
		for _, b := range edition.Editions() {
			t.Run(a.Name+"_to_"+b.Name, func(t *testing.T) {
				f := setup(t)
				l := issue(t, f)
				runtime, err := mcpgateway.NewRuntime([]mcpgateway.Binding{{Scope: f.scope, ServerID: "server", Client: f.rpc}})
				if err != nil {
					t.Fatal(err)
				}
				f.service, err = application.NewAgentGateway(tenants{f.access}, tenantFirstMCPAccess{f.access, t}, f.access, f.access, policySource{f}, runtime, f.store, func() time.Time { return f.now }, agentstore.NewID)
				if err != nil {
					t.Fatal(err)
				}
				foreign := agent.Scope{OrganizationID: "foreign_" + string(a.Key) + "_" + string(b.Key), RepositoryID: f.scope.RepositoryID}
				if _, err := f.store.Load(t.Context(), foreign, l.ID); err == nil {
					t.Fatal("lease namespace leak")
				}
				if _, err := f.service.Invoke(t.Context(), f.actor, foreign, l.ID, "same_call", f.call); err == nil {
					t.Fatal("foreign tool invocation")
				}
				if err := f.service.Revoke(t.Context(), f.actor, foreign, l.ID); err == nil {
					t.Fatal("foreign lease revoke")
				}
				stored, err := f.store.Load(t.Context(), f.scope, l.ID)
				if err != nil || stored.Revoked || stored.Uses != 0 || f.rpc.calls.Load() != 0 {
					t.Fatal("victim lease changed")
				}
			})
		}
	}
}
func TestMCPRejectsAmbiguousAuthorizationHeader(t *testing.T) {
	f := setup(t)
	h, _ := mcpgateway.NewHandler(f.service, auth{})
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("POST", "/v1/organizations/org/repositories/repo/agent/inventory", strings.NewReader(`{}`))
	req.Header.Add("Authorization", "Bearer valid")
	req.Header.Add("Authorization", "Bearer other_tenant")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
