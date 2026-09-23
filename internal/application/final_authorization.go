package application

import (
	"context"
	"safepatch/internal/domain"
	"safepatch/internal/proof"
	"safepatch/internal/trust"
	"time"
)

// AuthorizationSubject is assembled by the shared workflow after verification.
// Source adapters must validate provenance and bind every returned assessment to
// this subject. User requests and scanner/LLM claims cannot implement this port.
type AuthorizationSubject struct {
	Change                                            domain.Change
	RunID, IntentDigest, EvidenceDigest, PolicyDigest string
}
type AuthorizationAssessmentSource interface {
	AssessAuthorization(context.Context, AuthorizationSubject) (trust.Assessment, error)
}
type FinalAuthorizationConfig struct {
	Controls trust.Controls
	Source   AuthorizationAssessmentSource
}

func (w *ChangeWorkflow) finalAuthorization(ctx context.Context, change domain.Change, artifacts ChangeArtifacts, level proof.Level, complete bool, coverage trust.Coverage) (trust.KernelResult, error) {
	intentHash, err := artifacts.Intent.ContentHash()
	if err != nil {
		return trust.KernelResult{}, err
	}
	digest, err := trust.EvidenceDigest(artifacts.Evidence)
	if err != nil {
		return trust.KernelResult{}, err
	}
	controls := trust.DefaultControls(w.c.Target)
	assessment := trust.UnknownAssessment()
	if w.c.FinalAuthorization != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		controls = w.c.FinalAuthorization.Controls
		if w.c.FinalAuthorization.Source == nil {
			return trust.KernelResult{}, ErrChangeWorkflow
		}
		assessment, err = w.c.FinalAuthorization.Source.AssessAuthorization(ctx, AuthorizationSubject{change, artifacts.RunID, intentHash, digest, w.c.Policy.Hash()})
		if err != nil {
			return trust.KernelResult{}, ErrChangeWorkflow
		}
	}
	coverage.Complete = complete
	return (trust.TrustKernel{}).Evaluate(ctx, trust.KernelInput{Change: change, RunID: artifacts.RunID, AsOf: w.c.Now().UTC(), Intent: artifacts.Intent, Evidence: artifacts.Evidence, ProofLevel: level, Policy: w.c.Policy, Controls: controls, ProofCoverage: coverage, Assessment: assessment})
}
