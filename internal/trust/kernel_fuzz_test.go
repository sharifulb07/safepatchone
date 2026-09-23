package trust_test

import (
	"encoding/json"
	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/policy"
	"safepatch/internal/proof"
	"safepatch/internal/trust"
	"testing"
	"time"
)

func FuzzTrustKernel(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{255, 255, 255, 255, 255, 255, 255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 4096 {
			t.Skip()
		}
		in := kernelFixture(t)
		if len(b) >= 10 {
			*in.Risk = int(int8(b[0]))
			in.ProofLevel = proof.Level([]string{"L0", "L1", "L2", "L8", "UNKNOWN", "bad"}[int(b[1])%6])
			in.BusinessImpact = policy.Criticality([]string{"UNKNOWN", "LOW", "MEDIUM", "HIGH", "CRITICAL", "bad"}[int(b[2])%6])
			in.ProofCoverage.Complete = b[3]%2 == 0
			in.Provenance.Status = evidence.Status([]string{"PASS", "FAIL", "UNKNOWN", "ERROR", "SKIPPED", "bad"}[int(b[4])%6])
			in.Provenance.Origin = trust.Origin([]string{"VERIFIER", "LLM", "UNKNOWN", "bad"}[int(b[5])%4])
			in.ProofFreshness.ExpiresAt = in.AsOf.Add(time.Duration(int8(b[6])) * time.Second)
			in.Controls.MaxProofAgeSeconds = uint32(b[7])
			in.Controls.ReviewRiskAbove = int(b[8])
			in.Controls.EscrowImpactAt = policy.Criticality([]string{"LOW", "HIGH", "CRITICAL", "bad"}[int(b[9])%4])
		}
		k := trust.TrustKernel{}
		r, err := k.Evaluate(t.Context(), in)
		again, e := k.Evaluate(t.Context(), in)
		if (err == nil) != (e == nil) || !sameKernelResult(r, again) {
			t.Fatal("nondeterminism")
		}
		switch r.Decision {
		case trust.Authorize, trust.Review, trust.Escrow, trust.Block, trust.Reverify:
		default:
			t.Fatal("invalid decision")
		}
		if err != nil && r.Decision != trust.Block {
			t.Fatal("error failed open")
		}
		if r.Decision == trust.Authorize {
			if in.Risk == nil || *in.Risk > in.Controls.ReviewRiskAbove || in.Provenance.Origin != trust.OriginVerifier || in.Provenance.Status != evidence.StatusPass || !in.ProofCoverage.Complete || !in.AsOf.Before(in.ProofFreshness.ExpiresAt) || in.ProofLevel.Rank() < in.Controls.MinimumProofLevel.Rank() {
				t.Fatal("unsafe authorization")
			}
			in.ProofCoverage.Complete = false
			weakened, _ := k.Evaluate(t.Context(), in)
			if weakened.Decision == trust.Authorize {
				t.Fatal("coverage weakening authorized")
			}
			in.ProofCoverage.Complete = true
			in.Provenance.Origin = trust.OriginLLM
			poisoned, _ := k.Evaluate(t.Context(), in)
			if poisoned.Decision != trust.Block {
				t.Fatal("LLM authority")
			}
		}
	})
}

func FuzzTrustKernelUntrustedDocuments(f *testing.F) {
	fixture := kernelFixture(f)
	raw, _ := fixture.Evidence[0].CanonicalJSON()
	f.Add(uint8(0), raw)
	raw, _ = fixture.Intent.MarshalJSON()
	f.Add(uint8(1), raw)
	raw, _ = fixture.Policy.MarshalJSON()
	f.Add(uint8(2), raw)
	f.Add(uint8(0), []byte(`{"edition":"enterprise","status":"PASS"}`))
	f.Add(uint8(1), []byte(`null`))
	f.Fuzz(func(t *testing.T, kind uint8, raw []byte) {
		if len(raw) > 64*1024 {
			t.Skip()
		}
		in := kernelFixture(t)
		switch kind % 3 {
		case 0:
			node, err := evidence.DecodeEvidence(raw)
			if err != nil {
				return
			}
			in.Evidence = []evidence.Evidence{node}
		case 1:
			c, err := intent.DecodeIntentContract(raw)
			if err != nil {
				return
			}
			in.Intent = c
		case 2:
			p, err := policy.Decode(raw)
			if err != nil {
				return
			}
			in.Policy = p
		}
		before, _ := json.Marshal(in)
		r, err := (trust.TrustKernel{}).Evaluate(t.Context(), in)
		after, _ := json.Marshal(in)
		if string(before) != string(after) {
			t.Fatal("mutated untrusted input")
		}
		if err != nil && r.Decision != trust.Block {
			t.Fatal("error authorized")
		}
		if r.Decision == trust.Authorize {
			ih, _ := in.Intent.ContentHash()
			eh, _ := trust.EvidenceDigest(in.Evidence)
			if ih != in.Provenance.IntentDigest || eh != in.Provenance.EvidenceDigest || in.Policy.Hash() != in.Provenance.PolicyDigest {
				t.Fatal("substituted document authorized")
			}
		}
	})
}
