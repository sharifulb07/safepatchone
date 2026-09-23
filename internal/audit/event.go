package audit

import (
	"context"
	"errors"
	"time"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/tenant"
)

var ErrInvalid = errors.New("invalid audit record or query")
var ErrUnavailable = errors.New("audit storage unavailable")
var ErrConflict = errors.New("audit event already exists")

const MaxPage = 200

// Event is shared by all editions. Only trusted services append events. There
// is deliberately no arbitrary message/payload field for credentials or prompts.
type Event struct {
	ID             string                  `json:"id"`
	OrganizationID string                  `json:"organization_id"`
	Actor          authorization.Principal `json:"actor"`
	Action         string                  `json:"action"`
	ResourceID     string                  `json:"resource_id"`
	Outcome        string                  `json:"outcome"`
	OccurredAt     time.Time               `json:"occurred_at"`
}

func (e Event) Validate() error {
	if !tenant.ValidID(e.ID) || !tenant.ValidID(e.OrganizationID) || e.Actor.Validate() != nil || feature.ValidateKey(feature.Key(e.Action)) != nil || !tenant.ValidID(e.ResourceID) || e.OccurredAt.IsZero() || e.OccurredAt.Year() < 1 || e.OccurredAt.Year() > 9999 {
		return ErrInvalid
	}
	switch e.Outcome {
	case "SUCCEEDED", "DENIED", "FAILED", "UNKNOWN":
		return nil
	}
	return ErrInvalid
}

type Query struct {
	AfterID string `json:"after_id"`
	Limit   int    `json:"limit"`
}

func (q Query) Validate() error {
	if q.Limit < 1 || q.Limit > MaxPage || q.AfterID != "" && !tenant.ValidID(q.AfterID) {
		return ErrInvalid
	}
	return nil
}

type Page struct {
	Events      []Event `json:"events"`
	NextAfterID string  `json:"next_after_id,omitempty"`
}

// Appends are mandatory baseline security infrastructure, never premium-gated.
// Adapters bind both append and read to organization, enforce immutable IDs, and
// return ordered bounded pages. There is no entitlement-driven delete API.
type Store interface {
	Append(context.Context, string, Event) error
	List(context.Context, string, Query) (Page, error)
}
