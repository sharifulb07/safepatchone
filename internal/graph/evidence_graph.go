package graph

import (
	"context"
	"errors"
	"slices"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
)

type Relationship string

const (
	Supports        Relationship = "SUPPORTS"
	Contradicts     Relationship = "CONTRADICTS"
	DerivedFrom     Relationship = "DERIVED_FROM"
	Validates       Relationship = "VALIDATES"
	Invalidates     Relationship = "INVALIDATES"
	DependsOn       Relationship = "DEPENDS_ON"
	MaxRepositories              = 64
	MaxPageSize                  = 100
)

func Relationships() []Relationship {
	return []Relationship{Supports, Contradicts, DerivedFrom, Validates, Invalidates, DependsOn}
}
func (r Relationship) Valid() bool { return slices.Contains(Relationships(), r) }

var (
	ErrInvalidGraph = errors.New("invalid evidence graph request")
	ErrNotFound     = errors.New("evidence graph object not found in authorized scope")
	ErrConflict     = errors.New("immutable evidence graph identity conflict")
	ErrGraphData    = errors.New("invalid stored evidence graph data")
)

// Scope is short-lived trusted admission output, never a request authorization
// token. Explicit repository membership and the inclusive time window constrain
// both endpoints of every returned edge. Bounds use UTC microsecond precision;
// node timestamps are truncated to match. NotBefore year 1 means all history.
type Scope struct {
	TenantID      string
	RepositoryIDs []string
	NotBefore     time.Time
	AsOf          time.Time
}

func (s Scope) Validate() error {
	if !tenant.ValidID(s.TenantID) || len(s.RepositoryIDs) == 0 || len(s.RepositoryIDs) > MaxRepositories || s.AsOf.IsZero() || !validTime(s.AsOf) || !validTime(s.NotBefore) || s.NotBefore.After(s.AsOf) || s.AsOf.Nanosecond()%1000 != 0 || s.NotBefore.Nanosecond()%1000 != 0 {
		return ErrInvalidGraph
	}
	seen := map[string]bool{}
	for _, id := range s.RepositoryIDs {
		if !tenant.ValidID(id) || seen[id] {
			return ErrInvalidGraph
		}
		seen[id] = true
	}
	return nil
}
func validTime(t time.Time) bool { return t.UTC().Year() >= 1 && t.UTC().Year() <= 9999 }
func (s Scope) Contains(repository string, created time.Time) bool {
	created = created.UTC().Truncate(time.Microsecond)
	return slices.Contains(s.RepositoryIDs, repository) && !created.Before(s.NotBefore) && !created.After(s.AsOf)
}

// Edge is an immutable stored assertion. All relations are directed; cycles
// and distinct contradictory assertions are allowed. No relation authorizes
// production, changes node status, implies a reverse edge or proves causality.
type Edge struct {
	TenantID       string       `json:"tenant_id"`
	EdgeID         string       `json:"edge_id"`
	FromEvidenceID string       `json:"from_evidence_id"`
	ToEvidenceID   string       `json:"to_evidence_id"`
	Relationship   Relationship `json:"relationship"`
	CreatedAt      time.Time    `json:"created_at"`
}

func (e Edge) Validate() error {
	for _, id := range []string{e.TenantID, e.EdgeID, e.FromEvidenceID, e.ToEvidenceID} {
		if !evidence.ValidIdentifier(id) {
			return ErrInvalidGraph
		}
	}
	if e.FromEvidenceID == e.ToEvidenceID || !e.Relationship.Valid() || e.CreatedAt.IsZero() || !validTime(e.CreatedAt) {
		return ErrInvalidGraph
	}
	return nil
}

// Canonical keeps edge retry identity precise at PostgreSQL's microseconds.
func (e Edge) Canonical() (Edge, error) {
	if err := e.Validate(); err != nil {
		return Edge{}, err
	}
	e.CreatedAt = e.CreatedAt.UTC().Truncate(time.Microsecond)
	if e.CreatedAt.IsZero() {
		return Edge{}, ErrInvalidGraph
	}
	return e, nil
}

type Direction string

const (
	Outgoing Direction = "OUTGOING"
	Incoming Direction = "INCOMING"
	Both     Direction = "BOTH"
)

type Query struct {
	EvidenceID   string
	Direction    Direction
	Relationship Relationship // empty selects all six
	AfterEdgeID  string       // exclusive keyset cursor, not an authorization token
	Limit        int
}

func (q Query) Validate() error {
	if !evidence.ValidIdentifier(q.EvidenceID) || q.Limit < 1 || q.Limit > MaxPageSize || q.Relationship != "" && !q.Relationship.Valid() || q.AfterEdgeID != "" && !evidence.ValidIdentifier(q.AfterEdgeID) {
		return ErrInvalidGraph
	}
	if q.Direction != Outgoing && q.Direction != Incoming && q.Direction != Both {
		return ErrInvalidGraph
	}
	return nil
}

type Page struct {
	Edges      []Edge `json:"edges"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// EvidenceGraph is shared infrastructure. Trusted orchestration resolves source
// repository ownership and passes an admitted Scope. Implementations must reject
// replacement IDs, validate tenant identities and bound every adjacency query.
// Retention does not delete or rewrite history; there are no mutation/delete APIs.
type EvidenceGraph interface {
	PutEvidence(context.Context, Scope, string, evidence.Evidence) error
	GetEvidence(context.Context, Scope, string) (evidence.Evidence, error)
	AddRelationship(context.Context, Scope, Edge) error
	Neighbors(context.Context, Scope, Query) (Page, error)
}
