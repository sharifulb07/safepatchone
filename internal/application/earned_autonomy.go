package application

import (
	"context"
	"time"

	"safepatch/internal/agent"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/intent"
)

// AutonomySource is a trusted server boundary. Policy comes from operator
// approval; Proofs must load retained verifier results whose input provenance
// binds actor and challenge. Never implement this using request-supplied proof
// claims, MCP output, or an LLM decision. Missing/revoked evidence fails closed.
type AutonomySource interface {
	AutonomyPolicy(context.Context, agent.Scope, string) (agent.AutonomyPolicy, error)
	AutonomyProofs(context.Context, authorization.Principal, agent.Scope, string) ([]agent.AutonomyProof, error)
}

type EarnedAutonomyService struct {
	gateway *AgentGateway
	source  AutonomySource
}

func NewEarnedAutonomyService(gateway *AgentGateway, source AutonomySource) (*EarnedAutonomyService, error) {
	if gateway == nil || source == nil {
		return nil, ErrInvalidGuard
	}
	return &EarnedAutonomyService{gateway, source}, nil
}

func (s *EarnedAutonomyService) admit(ctx context.Context, actor authorization.Principal, scope agent.Scope, meter bool) (uint32, error) {
	if s == nil {
		return 0, agent.ErrUnavailable
	}
	if err := s.gateway.admit(ctx, actor, scope, true, false); err != nil {
		return 0, err
	}
	if err := stageResult(ctx, "autonomy entitlement", entitlement.RequireEntitlement(ctx, s.gateway.grants, scope.OrganizationID, feature.KeyAgentEarnedAutonomy)); err != nil {
		return 0, err
	}
	allowed, err := s.gateway.access.permissions.Allowed(ctx, actor, scope.OrganizationID, authorization.KeyAgentApprove)
	if err != nil || !allowed {
		return 0, agent.ErrDenied
	}
	limit, err := s.gateway.grants.Limit(ctx, scope.OrganizationID, feature.KeyAgentEarnedAutonomy)
	if err != nil || limit < 1 || limit > agent.MaxCalls || ctx.Err() != nil {
		return 0, agent.ErrDenied
	}
	if meter {
		if err := stageResult(ctx, "autonomy quota", s.gateway.access.quotas.Check(ctx, Execution{Actor: actor, Scope: scope.Tenant(), Requirement: Requirement{Feature: feature.KeyAgentEarnedAutonomy, Permission: authorization.KeyAgentApprove}})); err != nil {
			return 0, err
		}
	}
	return uint32(limit), nil
}

func (s *EarnedAutonomyService) policy(ctx context.Context, scope agent.Scope, id string) (agent.AutonomyPolicy, error) {
	if !evidence.ValidIdentifier(id) {
		return agent.AutonomyPolicy{}, agent.ErrInvalid
	}
	p, err := s.source.AutonomyPolicy(ctx, scope, id)
	if err != nil || p.ID != id || p.Policy.Scope != scope || p.Digest() == "" || ctx.Err() != nil {
		return agent.AutonomyPolicy{}, agent.ErrUnavailable
	}
	return p, nil
}

// Challenge supplies the exact input binding for the existing verification
// pipeline. Computing a challenge grants no authority to execute tools.
func (s *EarnedAutonomyService) Challenge(ctx context.Context, actor authorization.Principal, scope agent.Scope, policyID string, contract intent.IntentContract, calls []agent.Call) (string, error) {
	limit, err := s.admit(ctx, actor, scope, true)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p, err := s.policy(ctx, scope, policyID)
	if err != nil {
		return "", err
	}
	inv, err := s.gateway.runtime.Inventory(ctx, scope)
	if err != nil || ctx.Err() != nil {
		return "", agent.ErrUnavailable
	}
	_, challenge, err := (agent.AutonomyCompiler{}).Challenge(actor, contract, inv, p, calls, limit)
	return challenge, err
}

func (s *EarnedAutonomyService) evaluate(ctx context.Context, actor authorization.Principal, scope agent.Scope, policyID string, contract intent.IntentContract, calls []agent.Call, inv agent.Inventory, limit uint32) (agent.EarnedEvaluation, error) {
	p, err := s.policy(ctx, scope, policyID)
	if err != nil {
		return agent.EarnedEvaluation{}, err
	}
	_, challenge, err := (agent.AutonomyCompiler{}).Challenge(actor, contract, inv, p, calls, limit)
	if err != nil {
		return agent.EarnedEvaluation{}, err
	}
	records, err := s.source.AutonomyProofs(ctx, actor, scope, challenge)
	if err != nil || ctx.Err() != nil {
		return agent.EarnedEvaluation{}, agent.ErrUnavailable
	}
	return (agent.AutonomyCompiler{}).Evaluate(ctx, actor, contract, inv, p, calls, limit, records, s.gateway.now().UTC().Truncate(time.Microsecond))
}

func (s *EarnedAutonomyService) Issue(ctx context.Context, actor authorization.Principal, scope agent.Scope, policyID string, contract intent.IntentContract, calls []agent.Call) (agent.SafeLease, error) {
	limit, err := s.admit(ctx, actor, scope, true)
	if err != nil {
		return agent.SafeLease{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	inv, err := s.gateway.runtime.Inventory(ctx, scope)
	if err != nil {
		return agent.SafeLease{}, agent.ErrUnavailable
	}
	evaluated, err := s.evaluate(ctx, actor, scope, policyID, contract, calls, inv, limit)
	if err != nil {
		return agent.SafeLease{}, err
	}
	current, err := s.admit(ctx, actor, scope, false)
	if err != nil || current != limit {
		return agent.SafeLease{}, agent.ErrDenied
	}
	id, err := s.gateway.id()
	if err != nil {
		return agent.SafeLease{}, agent.ErrUnavailable
	}
	lease, err := agent.NewSafeLease(id, actor, evaluated.Capabilities, s.gateway.now())
	if err != nil || lease.ExpiresAt.After(evaluated.ValidUntil) {
		return agent.SafeLease{}, agent.ErrDenied
	}
	if err := s.gateway.leases.Create(ctx, lease); err != nil {
		return agent.SafeLease{}, agent.ErrUnavailable
	}
	return lease, nil
}

// Invoke revalidates authoritative proof and current policy on every use. The
// exact original proposal is supplied again and must match the sealed binding.
func (s *EarnedAutonomyService) Invoke(ctx context.Context, actor authorization.Principal, scope agent.Scope, leaseID, invocationID string, contract intent.IntentContract, calls []agent.Call, call agent.Call) (AgentResult, error) {
	unknown := AgentResult{Receipt: agent.Receipt{Outcome: agent.OutcomeUnknown}}
	if _, err := s.admit(ctx, actor, scope, true); err != nil {
		return unknown, err
	}
	return s.gateway.invoke(ctx, actor, scope, leaseID, invocationID, call, func(ctx context.Context, lease agent.SafeLease, inv agent.Inventory) (string, error) {
		limit, err := s.admit(ctx, actor, scope, false)
		if err != nil {
			return "", err
		}
		fields, err := lease.Capabilities.Fields()
		if err != nil || fields.Earned == nil {
			return "", agent.ErrDenied
		}
		evaluated, err := s.evaluate(ctx, actor, scope, fields.Earned.PolicyID, contract, calls, inv, limit)
		if err != nil || evaluated.Capabilities.Digest() != lease.Capabilities.Digest() || lease.ExpiresAt.After(evaluated.ValidUntil) {
			return "", agent.ErrDenied
		}
		current, err := s.admit(ctx, actor, scope, false)
		if err != nil || current != limit {
			return "", agent.ErrDenied
		}
		return fields.PolicyDigest, nil
	})
}
