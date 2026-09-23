package agent_test

import (
	"context"
	"encoding/json"
	"safepatch/internal/agent"
	"safepatch/internal/authorization"
	"safepatch/internal/domain"
	"safepatch/internal/evidence"
	"safepatch/internal/proof"
	"safepatch/internal/proof/passport"
	"safepatch/internal/trust"
	"strings"
	"testing"
	"time"
)

func TestAutonomyCompilerProofBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "insufficient", "unknown", "failed", "policy_unknown", "low_level", "actor", "challenge", "tenant", "repository", "intent", "expired", "future", "stale", "duplicate", "duplicate_run", "revoked", "policy_drift", "call_drift", "limit_drift", "zero_limit", "production", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			inv, policy, contract, call := fixture(t)
			p := agent.AutonomyPolicy{ID: "reviewed", Policy: policy, MinimumProofs: 1, MinimumLevel: proof.L2, MaxProofAgeSeconds: 3600}
			if mode == "insufficient" {
				p.MinimumProofs = 2
			}
			actor := authorization.Principal{ID: "actor", Kind: authorization.ServiceAccount}
			now := time.Now().UTC().Truncate(time.Microsecond)
			k := agent.AutonomyCompiler{}
			_, challenge, err := k.Challenge(actor, contract, inv, p, []agent.Call{call}, 2)
			if err != nil {
				t.Fatal(err)
			}
			change := domain.Change{OrganizationID: "org", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
			hash := evidence.Digest([]byte("retained evidence"))
			cap, _ := agent.Compile(contract, inv, policy, []agent.Call{call})
			cf, _ := cap.Fields()
			f := passport.Fields{Change: change, ChangeID: change.ID(), RunID: "run", IntentDigest: cf.IntentDigest, Commit: change.HeadSHA, ProofLevel: proof.L2, ProofPlanDigest: hash, PolicyDecision: passport.PolicyDecision{PolicyDigest: hash, ResultDigest: hash, Status: evidence.StatusPass}, EvidenceGraphDigest: hash, VerificationDecision: trust.Pass, IssuedAt: now.Add(-time.Minute)}
			limit := uint32(2)
			record := agent.AutonomyProof{Actor: actor, ChallengeDigest: challenge, ValidUntil: now.Add(time.Hour)}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "unknown":
				f.VerificationDecision = trust.Unknown
			case "failed":
				f.VerificationDecision = trust.Fail
			case "policy_unknown":
				f.PolicyDecision.Status = evidence.StatusUnknown
				f.VerificationDecision = trust.Unknown
			case "low_level":
				f.ProofLevel = proof.L1
			case "actor":
				record.Actor.ID = "other"
			case "challenge":
				record.ChallengeDigest = hash
			case "tenant":
				f.Change.OrganizationID = "other"
				f.ChangeID = f.Change.ID()
			case "repository":
				f.Change.RepositoryID = "other"
				f.ChangeID = f.Change.ID()
			case "intent":
				f.IntentDigest = hash
			case "expired":
				record.ValidUntil = now
			case "future":
				f.IssuedAt = now.Add(time.Minute)
			case "stale":
				f.IssuedAt = now.Add(-time.Hour)
			case "policy_drift":
				p.Policy.Revision++
			case "call_drift":
				call.Arguments = json.RawMessage(`{"path":"other"}`)
			case "limit_drift":
				limit = 1
			case "zero_limit":
				limit = 0
			case "production":
				p.Policy.Rules[0].Effect = "WRITE_PRODUCTION"
			case "cancelled":
				cancel()
			}
			record.Passport, err = passport.New(f)
			if err != nil {
				t.Fatal(err)
			}
			records := []agent.AutonomyProof{record}
			switch mode {
			case "missing", "revoked":
				records = nil
			case "duplicate":
				records = append(records, record)
			case "duplicate_run":
				f.IssuedAt = f.IssuedAt.Add(-time.Second)
				other := record
				other.Passport, _ = passport.New(f)
				records = append(records, other)
			}
			result, err := k.Evaluate(ctx, actor, contract, inv, p, []agent.Call{call}, limit, records, now)
			if mode != "valid" {
				if err == nil || result.Capabilities.Digest() != "" {
					t.Fatal("untrusted escalation", mode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			fields, _ := result.Capabilities.Fields()
			if fields.Earned == nil || fields.Earned.ChallengeDigest != challenge {
				t.Fatal("missing binding")
			}
			fields.Earned.PolicyID = "tamper"
			fresh, _ := result.Capabilities.Fields()
			if fresh.Earned.PolicyID != p.ID {
				t.Fatal("mutable binding")
			}
			raw, _ := result.Capabilities.MarshalJSON()
			decoded, err := agent.DecodeCapabilities(raw)
			if err != nil || decoded.Digest() != result.Capabilities.Digest() {
				t.Fatal("binding round trip", err)
			}
			for _, bad := range []string{`null`, `{}`, `{"policy_id":"../escape","challenge_digest":"bad","proof_set_digest":"bad"}`} {
				var obj map[string]json.RawMessage
				_ = json.Unmarshal(raw, &obj)
				obj["earned"] = json.RawMessage(bad)
				poisoned, _ := json.Marshal(obj)
				if _, err := agent.DecodeCapabilities(poisoned); err == nil {
					t.Fatal("invalid earned binding accepted")
				}
			}
			// Multiple distinct runs are canonical regardless of storage return order.
			second := record
			f.RunID = "run_2"
			second.Passport, _ = passport.New(f)
			a, err := k.Evaluate(ctx, actor, contract, inv, p, []agent.Call{call}, limit, []agent.AutonomyProof{record, second}, now)
			if err != nil {
				t.Fatal(err)
			}
			b, err := k.Evaluate(ctx, actor, contract, inv, p, []agent.Call{call}, limit, []agent.AutonomyProof{second, record}, now)
			if err != nil || a.Capabilities.Digest() != b.Capabilities.Digest() {
				t.Fatal("proof order changed binding")
			}
		})
	}
}
