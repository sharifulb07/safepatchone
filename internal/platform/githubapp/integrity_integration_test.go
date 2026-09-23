package githubapp_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/trust"
	"safepatch/internal/verification"
)

func TestIntegrityBlocksWeakeningBeforeAnyExecutionInEveryEdition(t *testing.T) {
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			for _, mode := range []string{"scanner", "test", "coverage", "policy", "exclusion", "timeout", "incomplete", "source_error", "actual_timeout"} {
				t.Run(mode, func(t *testing.T) {
					core, change, source, runner, archive, _ := workflowFixture(t)
					actor := authorization.Principal{ID: "user", Kind: authorization.User}
					// Establish retained history using only verify.basic, with no premium grant.
					if _, err := core.Run(t.Context(), actor, change, "prior"); err != nil {
						t.Fatal(err)
					}
					prior, _ := json.Marshal(archive.saved)
					baseRaw, _ := json.Marshal(source.integrity.Base)
					b, _ := source.integrity.Base.Fields()
					h, _ := source.integrity.Head.Fields()
					switch mode {
					case "scanner":
						h.Scanners = []verification.ControlCheck{}
					case "test":
						b.Tests = []verification.ControlCheck{{ID: "go.test", Enabled: true, ConfigDigest: b.Scanners[0].ConfigDigest}}
						h.Tests = []verification.ControlCheck{{ID: "go.test", Enabled: false, ConfigDigest: b.Scanners[0].ConfigDigest}}
					case "coverage":
						threshold := uint32(8000)
						b.CoverageThresholdBPS = &threshold
						low := uint32(7000)
						h.CoverageThresholdBPS = &low
					case "policy":
						h.Policy = bytes.Replace(h.Policy, []byte(`"producer":"go"`), []byte(`"producer":"other"`), 1)
					case "exclusion":
						h.SecurityExclusions = []string{"security/**"}
					case "timeout":
						h.TimeoutMillis = 1000
					case "incomplete":
						h.Complete = false
					case "source_error":
						source.integrityError = true
					case "actual_timeout":
						source.facts.Execution.Limits.Timeout = time.Second
					}
					source.integrity.Base, _ = verification.NewVerificationControls(b)
					source.integrity.Head, _ = verification.NewVerificationControls(h)
					for _, hosted := range []bool{false, true} {
						var err error
						if hosted {
							_, err = core.RunHosted(t.Context(), actor, change, "blocked")
						} else {
							_, err = core.Run(t.Context(), actor, change, "blocked")
						}
						if err == nil {
							t.Fatal("weakening allowed", mode)
						}
					}
					after, _ := json.Marshal(archive.saved)
					if runner.calls != 1 || !bytes.Equal(prior, after) {
						t.Fatal("blocked run executed or changed history")
					}
					if len(baseRaw) == 0 {
						t.Fatal("missing baseline")
					}
				})
			}
		})
	}
}
func TestIndependentReviewAndSelfApprovalBinding(t *testing.T) {
	for _, mode := range []string{"approved", "self", "author", "llm", "expired", "permission", "revoked", "review_replaced", "candidate_policy", "missing_plan_check"} {
		t.Run(mode, func(t *testing.T) {
			core, change, source, runner, archive, access := workflowFixture(t)
			h, _ := source.integrity.Head.Fields()
			h.TimeoutMillis = 120000
			source.integrity.Head, _ = verification.NewVerificationControls(h)
			source.integrity.Authors = []string{"author"}
			source.integrity.AuthorsComplete = true
			now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
			source.integrity.Review = trust.ControlReview{ID: "review", ChangeID: change.ID(), BaseDigest: source.integrity.Base.Digest(), HeadDigest: source.integrity.Head.Digest(), ReviewerID: "reviewer", Origin: "HUMAN_SESSION", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
			switch mode {
			case "self":
				source.integrity.Review.ReviewerID = "user"
			case "author":
				source.integrity.Review.ReviewerID = "author"
			case "llm":
				source.integrity.Review.Origin = "LLM"
			case "expired":
				source.integrity.Review.ExpiresAt = now
			case "permission":
				access.denyReview = true
			case "revoked":
				source.onIntegrity = func(n int) {
					if n >= 2 {
						source.integrityError = true
					}
				}
			case "review_replaced":
				source.onIntegrity = func(n int) {
					if n >= 2 {
						source.integrity.Review.ID = "replacement"
					}
				}
			case "candidate_policy":
				b, _ := source.integrity.Base.Fields()
				b.Policy = bytes.Replace(b.Policy, []byte(`"producer":"go"`), []byte(`"producer":"other"`), 1)
				source.integrity.Base, _ = verification.NewVerificationControls(b)
			case "missing_plan_check":
				b, _ := source.integrity.Base.Fields()
				b.Scanners[0].ID = "security.scan"
				source.integrity.Base, _ = verification.NewVerificationControls(b)
				h = b
				h.Subject.Revision = change.HeadSHA
				source.integrity.Head, _ = verification.NewVerificationControls(h)
			}
			r, err := core.RunHosted(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, change, "run")
			if mode == "approved" {
				if err != nil || r.Decision != trust.Pass || len(archive.saved) != 1 || archive.saved[0].ControlReviewID != "review" {
					t.Fatal(r, err)
				}
				fields, _ := archive.saved[0].Evidence[0].Fields()
				if fields.InputHashes["verification_control_review"] != source.integrity.Review.Digest() {
					t.Fatal("review not bound into evidence")
				}
				return
			}
			if err == nil || r.Decision == trust.Pass || runner.calls != 0 {
				t.Fatal("guard bypass", mode, r, err, runner.calls)
			}
		})
	}
}
func TestIntegritySourceCannotBeOmitted(t *testing.T) {
	var config application.ChangeWorkflowConfig
	workflowFixture(t, func(c *application.ChangeWorkflowConfig) { config = *c })
	config.Integrity = nil
	if _, err := application.NewChangeWorkflow(config); err == nil {
		t.Fatal("optional integrity")
	}
}
