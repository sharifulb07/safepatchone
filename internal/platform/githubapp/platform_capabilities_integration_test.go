package githubapp_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/audit"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
)

type sharedAudit struct {
	events []audit.Event
	fail   bool
}

func (s *sharedAudit) Append(_ context.Context, org string, e audit.Event) error {
	if s.fail {
		return errors.New("audit unavailable")
	}
	if e.Validate() != nil || e.OrganizationID != org {
		return audit.ErrInvalid
	}
	s.events = append(s.events, e)
	return nil
}
func (s *sharedAudit) List(context.Context, string, audit.Query) (audit.Page, error) {
	return audit.Page{Events: append([]audit.Event{}, s.events...)}, nil
}
func TestDeploymentUsesSameWorkflowAndBaselineAuditAcrossGrantChanges(t *testing.T) {
	actor := authorization.Principal{ID: "user", Kind: authorization.User}
	for _, airgap := range []bool{false, true} {
		store := &sharedAudit{}
		grants := flowEntitlements{enabled: map[feature.Key]bool{feature.KeyEnterpriseSelfHosted: true, feature.KeyEnterpriseAirgap: true}}
		permissions := &changeAccess{allowed: true, quota: true}
		deployment, _ := application.NewDeploymentAccess(application.PlatformAccess{Tenants: tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
			return s, nil
		}), Entitlements: grants, Permissions: permissions, Quotas: permissions}, airgap)
		core, change, _, runner, archive, _ := workflowFixture(t, func(c *application.ChangeWorkflowConfig) { c.Deployment = deployment; c.Audit = store })
		result, err := core.RunHosted(t.Context(), actor, change, "run")
		if err != nil || result.Decision != trust.Pass || len(store.events) != 1 {
			t.Fatal(result, err)
		}
		plain, plainChange, _, _, plainArchive, _ := workflowFixture(t)
		other, err := plain.Run(t.Context(), actor, plainChange, "run")
		if err != nil || result != other || !reflect.DeepEqual(archive.saved, plainArchive.saved) {
			t.Fatal("separate enterprise core")
		}
		prior := archive.saved[0]
		calls := runner.calls
		grants.enabled[feature.KeyEnterpriseSelfHosted] = false
		denied, err := core.RunHosted(t.Context(), actor, change, "denied")
		if err == nil || denied.Decision != trust.Unknown || runner.calls != calls || len(store.events) != 2 {
			t.Fatal("downgrade bypass or capture disabled")
		}
		if !reflect.DeepEqual(prior, archive.saved[0]) {
			t.Fatal("downgrade corrupted proof history")
		}
		grants.enabled[feature.KeyEnterpriseSelfHosted] = true
		store.fail = true
		failed, err := core.RunHosted(t.Context(), actor, change, "audit_failure")
		if err == nil || failed.Decision != trust.Unknown {
			t.Fatal("audit failure emitted PASS")
		}
	}
}

func TestDeploymentRevocationIsRecheckedAtActualRunnerDispatch(t *testing.T) {
	grants := flowEntitlements{enabled: map[feature.Key]bool{feature.KeyEnterpriseSelfHosted: true}}
	permissions := &changeAccess{allowed: true, quota: true}
	access, _ := application.NewDeploymentAccess(application.PlatformAccess{Tenants: tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), Entitlements: grants, Permissions: permissions, Quotas: permissions}, false)
	core, change, source, runner, _, _ := workflowFixture(t, func(c *application.ChangeWorkflowConfig) { c.Deployment = access })
	source.onIntegrity = func(n int) {
		if n == 2 {
			grants.enabled[feature.KeyEnterpriseSelfHosted] = false
		}
	}
	result, _ := core.RunHosted(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, change, "revoked")
	if result.Decision == trust.Pass || runner.calls != 0 {
		t.Fatal("revoked deployment ran verifier", result, runner.calls)
	}
}
