package trust

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"safepatch/internal/domain"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
)

var ErrSelfApproval = errors.New("independent verification-control review required")

type ControlReview struct {
	ID         string    `json:"id"`
	ChangeID   string    `json:"change_id"`
	BaseDigest string    `json:"base_digest"`
	HeadDigest string    `json:"head_digest"`
	ReviewerID string    `json:"reviewer_id"`
	Origin     string    `json:"origin"` // HUMAN_SESSION only; never an LLM or service result
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Digest binds immutable review provenance into evidence; it is not a signature.
func (r ControlReview) Digest() string {
	raw, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return evidence.Digest(raw)
}

// SelfApprovalGuard validates trusted review provenance, not user-submitted
// assertions of identity. Membership and policy.manage are checked by admission.
type SelfApprovalGuard struct{}

func (SelfApprovalGuard) Check(ctx context.Context, change domain.Change, baseDigest, headDigest, requester string, authors []string, authorsComplete bool, review ControlReview, now time.Time) error {
	if ctx == nil {
		return ErrSelfApproval
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if change.Validate() != nil || !tenant.ValidID(requester) || !authorsComplete || len(authors) == 0 || len(authors) > 256 || !evidence.ValidIdentifier(review.ID) || !tenant.ValidID(review.ReviewerID) || review.Origin != "HUMAN_SESSION" || review.ChangeID != change.ID() || review.BaseDigest != baseDigest || review.HeadDigest != headDigest || review.ReviewerID == requester {
		return ErrSelfApproval
	}
	for _, h := range []string{baseDigest, headDigest} {
		if (evidence.OriginalReference{SHA256: h}).Validate() != nil {
			return ErrSelfApproval
		}
	}
	seen := map[string]bool{}
	for _, a := range authors {
		if !tenant.ValidID(a) || seen[a] {
			return ErrSelfApproval
		}
		seen[a] = true
	}
	if slices.Contains(authors, review.ReviewerID) {
		return ErrSelfApproval
	}
	if now.IsZero() || review.IssuedAt.IsZero() || review.ExpiresAt.IsZero() || review.IssuedAt.After(now) || !now.Before(review.ExpiresAt) || !review.ExpiresAt.After(review.IssuedAt) || review.ExpiresAt.Sub(review.IssuedAt) > 24*time.Hour {
		return ErrSelfApproval
	}
	return nil
}
