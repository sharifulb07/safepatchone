package application

import (
	"context"
	"encoding/json"
	"time"

	"safepatch/internal/agent"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/intent"
)

// AgentRuntime uses deployment-bound server identities and bounded transports.
// Inventory descriptions and Call output are always untrusted. No transport may
// retry tools/call, follow tool-supplied URLs, or execute local shell commands.
type AgentRuntime interface {
	Inventory(context.Context, agent.Scope) (agent.Inventory, error)
	Call(context.Context, agent.Scope, agent.Call) (json.RawMessage, error)
}

// AgentPolicySource reads current operator-owned rules. It must never load rules
// from prompts, tool metadata, repository content or request bodies.
type AgentPolicySource interface {
	Policy(context.Context, agent.Scope) (agent.Policy, error)
}
type AgentGateway struct {
	access   *ExecutionGuard
	grants   entitlement.EntitlementService
	policies AgentPolicySource
	runtime  AgentRuntime
	leases   agent.LeaseStore
	now      func() time.Time
	id       func() (string, error)
}

func NewAgentGateway(tenants TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, policies AgentPolicySource, runtime AgentRuntime, leases agent.LeaseStore, now func() time.Time, id func() (string, error)) (*AgentGateway, error) {
	if policies == nil || runtime == nil || leases == nil || now == nil || id == nil {
		return nil, ErrInvalidGuard
	}
	g, err := NewExecutionGuard(tenants, grants, permissions, quotas, Requirement{Feature: feature.KeyAgentMCPInventory, Permission: authorization.KeyAgentManage})
	if err != nil {
		return nil, err
	}
	return &AgentGateway{g, grants, policies, runtime, leases, now, id}, nil
}
func (s *AgentGateway) admit(ctx context.Context, actor authorization.Principal, scope agent.Scope, full, meter bool) error {
	if s == nil {
		return agent.ErrUnavailable
	}
	// Resolve tenant then every required entitlement before actor authorization.
	if ctx == nil || actor.Validate() != nil || scope.Validate() != nil {
		return agent.ErrInvalid
	}
	resolved, err := s.access.tenants.Resolve(ctx, actor, scope.Tenant())
	if err == nil && resolved != scope.Tenant() {
		err = agent.ErrDenied
	}
	if err := stageResult(ctx, "agent tenant", err); err != nil {
		return err
	}
	keys := []feature.Key{feature.KeyAgentMCPInventory}
	if full {
		keys = append(keys, feature.KeyAgentMCPGateway, feature.KeyAgentSafeLease, feature.KeyAgentTemporalGuard)
	}
	for _, key := range keys {
		if err := stageResult(ctx, "agent entitlement", entitlement.RequireEntitlement(ctx, s.grants, scope.OrganizationID, key)); err != nil {
			return err
		}
	}
	allowed, err := s.access.permissions.Allowed(ctx, actor, scope.OrganizationID, authorization.KeyAgentManage)
	if err == nil && !allowed {
		err = ErrPermissionDenied
	}
	if err := stageResult(ctx, "agent permission", err); err != nil {
		return err
	}
	if meter {
		return stageResult(ctx, "agent quota", s.access.quotas.Check(ctx, Execution{Actor: actor, Scope: scope.Tenant(), Requirement: s.access.requirement}))
	}
	return nil
}
func (s *AgentGateway) Inventory(ctx context.Context, actor authorization.Principal, scope agent.Scope) (agent.Inventory, error) {
	if err := s.admit(ctx, actor, scope, false, true); err != nil {
		return agent.Inventory{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	inv, err := s.runtime.Inventory(ctx, scope)
	f, fe := inv.Fields()
	if err != nil || fe != nil || f.Scope != scope || ctx.Err() != nil {
		return agent.Inventory{}, agent.ErrUnavailable
	}
	return inv, nil
}
func (s *AgentGateway) compile(ctx context.Context, scope agent.Scope, contract intent.IntentContract, calls []agent.Call) (agent.Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	inv, err := s.runtime.Inventory(ctx, scope)
	if err != nil {
		return agent.Capabilities{}, agent.ErrUnavailable
	}
	p, err := s.policies.Policy(ctx, scope)
	if err != nil || p.Scope != scope || ctx.Err() != nil {
		return agent.Capabilities{}, agent.ErrUnavailable
	}
	return agent.Compile(contract, inv, p, calls)
}
func (s *AgentGateway) Compile(ctx context.Context, actor authorization.Principal, scope agent.Scope, contract intent.IntentContract, calls []agent.Call) (agent.Capabilities, error) {
	if err := s.admit(ctx, actor, scope, false, true); err != nil {
		return agent.Capabilities{}, err
	}
	return s.compile(ctx, scope, contract, calls)
}
func (s *AgentGateway) Issue(ctx context.Context, actor authorization.Principal, scope agent.Scope, contract intent.IntentContract, calls []agent.Call) (agent.SafeLease, error) {
	if err := s.admit(ctx, actor, scope, true, true); err != nil {
		return agent.SafeLease{}, err
	}
	c, err := s.compile(ctx, scope, contract, calls)
	if err != nil {
		return agent.SafeLease{}, err
	}
	if err := s.admit(ctx, actor, scope, true, false); err != nil {
		return agent.SafeLease{}, err
	}
	id, err := s.id()
	if err != nil {
		return agent.SafeLease{}, agent.ErrUnavailable
	}
	l, err := agent.NewSafeLease(id, actor, c, s.now())
	if err != nil {
		return agent.SafeLease{}, err
	}
	if err := s.leases.Create(ctx, l); err != nil {
		return agent.SafeLease{}, agent.ErrUnavailable
	}
	return l, nil
}

type AgentResult struct {
	Receipt agent.Receipt `json:"receipt"`
	// Output is untrusted MCP content, not a verification result or approval.
	Output json.RawMessage `json:"output"`
}

func (s *AgentGateway) Invoke(ctx context.Context, actor authorization.Principal, scope agent.Scope, leaseID, invocationID string, call agent.Call) (AgentResult, error) {
	return s.invoke(ctx, actor, scope, leaseID, invocationID, call, nil)
}

// A refresh hook exists only for server-composed proof-based admission. Both
// paths still consume SafeLease through the same TemporalGuard and dispatcher.
func (s *AgentGateway) invoke(ctx context.Context, actor authorization.Principal, scope agent.Scope, leaseID, invocationID string, call agent.Call, refresh func(context.Context, agent.SafeLease, agent.Inventory) (string, error)) (AgentResult, error) {
	result := AgentResult{Receipt: agent.Receipt{Outcome: agent.OutcomeUnknown}}
	if !evidence.ValidIdentifier(leaseID) || !evidence.ValidIdentifier(invocationID) {
		return result, agent.ErrInvalid
	}
	call, err := call.Canonical()
	if err != nil {
		return result, err
	}
	if err := s.admit(ctx, actor, scope, true, true); err != nil {
		return result, err
	}
	l, err := s.leases.Load(ctx, scope, leaseID)
	if err != nil || l.Validate() != nil || l.Actor != actor {
		return result, agent.ErrDenied
	}
	fields, _ := l.Capabilities.Fields()
	if (fields.Earned != nil) != (refresh != nil) {
		return result, agent.ErrDenied
	}
	// Invocation cannot outlive its lease or the gateway's bounded call window.
	deadline := s.now().Add(30 * time.Second)
	if l.ExpiresAt.Before(deadline) {
		deadline = l.ExpiresAt
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	inv, err := s.runtime.Inventory(ctx, scope)
	if err != nil {
		return result, agent.ErrUnavailable
	}
	var policyDigest string
	if refresh != nil {
		policyDigest, err = refresh(ctx, l, inv)
	} else {
		var p agent.Policy
		p, err = s.policies.Policy(ctx, scope)
		if p.Scope == scope {
			policyDigest = p.Digest()
		}
	}
	if err != nil || policyDigest == "" {
		return result, agent.ErrUnavailable
	}
	if err := s.admit(ctx, actor, scope, true, false); err != nil {
		return result, err
	}
	err = s.leases.Consume(ctx, scope, leaseID, invocationID, call.Digest(), func(current *agent.SafeLease) error {
		now := s.now().UTC().Truncate(time.Microsecond)
		if err := (agent.TemporalGuard{}).Check(*current, actor, scope, call, inv.Digest(), policyDigest, now); err != nil {
			return err
		}
		current.Uses++
		current.LastUsedAt = now
		return nil
	})
	if err != nil {
		return result, agent.ErrDenied
	}
	if ctx.Err() != nil {
		return result, agent.ErrUnavailable
	} // consumed, never replay
	raw, err := s.runtime.Call(ctx, scope, call)
	if err != nil || ctx.Err() != nil || !s.now().Before(l.ExpiresAt) {
		return result, agent.ErrUnavailable
	}
	checked, err := agent.CanonicalObject(raw, 64*1024)
	if err != nil {
		return result, agent.ErrUnavailable
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(checked, &obj) != nil || len(obj["content"]) == 0 {
		return result, agent.ErrUnavailable
	}
	var content []json.RawMessage
	if json.Unmarshal(obj["content"], &content) != nil || content == nil || len(content) > 128 {
		return result, agent.ErrUnavailable
	}
	failed := false
	if value, ok := obj["isError"]; ok {
		if string(value) == "null" || json.Unmarshal(value, &failed) != nil {
			return result, agent.ErrUnavailable
		}
	}
	outcome := agent.OutcomeReturned
	if failed {
		outcome = agent.OutcomeToolError
	}
	receipt := agent.Receipt{Outcome: outcome, ResultDigest: evidence.Digest(checked)}
	if err := s.leases.Finish(ctx, scope, leaseID, invocationID, receipt); err != nil {
		return result, agent.ErrUnavailable
	}
	return AgentResult{Receipt: receipt, Output: checked}, nil
}

// Revocation remains available after losing premium entitlements. It only
// removes authority and still requires current tenant ownership/agent.manage.
func (s *AgentGateway) Revoke(ctx context.Context, actor authorization.Principal, scope agent.Scope, id string) error {
	if s == nil || ctx == nil || actor.Validate() != nil || scope.Validate() != nil || !evidence.ValidIdentifier(id) {
		return agent.ErrInvalid
	}
	resolved, err := s.access.tenants.Resolve(ctx, actor, scope.Tenant())
	if err != nil || resolved != scope.Tenant() {
		return agent.ErrDenied
	}
	allowed, err := s.access.permissions.Allowed(ctx, actor, scope.OrganizationID, authorization.KeyAgentManage)
	if err != nil || !allowed || ctx.Err() != nil {
		return agent.ErrDenied
	}
	if err := s.leases.Revoke(ctx, scope, id); err != nil {
		return agent.ErrUnavailable
	}
	return nil
}
