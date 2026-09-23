package trust_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"safepatch/internal/domain"
	"safepatch/internal/evidence"
	"safepatch/internal/trust"
)

func TestSelfApprovalGuard(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	change := domain.Change{OrganizationID: "org", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	hash := evidence.Digest([]byte("controls"))
	for _, mode := range []string{"independent", "self", "author", "llm", "service", "expired", "future", "replayed_change", "replayed_controls", "incomplete_authors", "duplicate_authors", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			review := trust.ControlReview{ID: "review", ChangeID: change.ID(), BaseDigest: hash, HeadDigest: hash, ReviewerID: "reviewer", Origin: "HUMAN_SESSION", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
			authors := []string{"author"}
			complete := true
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "self":
				review.ReviewerID = "requester"
			case "author":
				review.ReviewerID = "author"
			case "llm":
				review.Origin = "LLM"
			case "service":
				review.Origin = "SERVICE_ACCOUNT"
			case "expired":
				review.ExpiresAt = now
			case "future":
				review.IssuedAt = now.Add(time.Minute)
			case "replayed_change":
				review.ChangeID = "other"
			case "replayed_controls":
				review.HeadDigest = evidence.Digest([]byte("other"))
			case "incomplete_authors":
				complete = false
			case "duplicate_authors":
				authors = append(authors, "author")
			case "cancel":
				cancel()
			}
			err := (trust.SelfApprovalGuard{}).Check(ctx, change, hash, hash, "requester", authors, complete, review, now)
			if (mode == "independent") != (err == nil) {
				t.Fatal(mode, err)
			}
		})
	}
}
