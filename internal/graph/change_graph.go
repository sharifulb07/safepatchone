package graph

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
)

const (
	ChangeNodeSchema     = "safepatch.changegraph.node.v1"
	ChangeLinkSchema     = "safepatch.changegraph.link.v1"
	MaxChangeObjectBytes = 128 * 1024
)

type EntityKind string

const (
	FileEntity       EntityKind = "file"
	SymbolEntity     EntityKind = "symbol"
	APIEntity        EntityKind = "api"
	DBEntity         EntityKind = "db"
	EventEntity      EntityKind = "event"
	ServiceEntity    EntityKind = "service"
	TestEntity       EntityKind = "test"
	DependencyEntity EntityKind = "dependency"
	RuntimeEntity    EntityKind = "runtime"
	IncidentEntity   EntityKind = "incident"
)

func EntityKinds() []EntityKind {
	return []EntityKind{FileEntity, SymbolEntity, APIEntity, DBEntity, EventEntity, ServiceEntity, TestEntity, DependencyEntity, RuntimeEntity, IncidentEntity}
}

type CausalRelation string

const (
	Contains        CausalRelation = "CONTAINS"
	Calls           CausalRelation = "CALLS"
	Reads           CausalRelation = "READS"
	Writes          CausalRelation = "WRITES"
	Publishes       CausalRelation = "PUBLISHES"
	Consumes        CausalRelation = "CONSUMES"
	CausalDependsOn CausalRelation = "DEPENDS_ON"
	Tests           CausalRelation = "TESTS"
	RuntimeCalls    CausalRelation = "RUNTIME_CALLS"
	Causes          CausalRelation = "CAUSES"
)

func CausalRelations() []CausalRelation {
	return []CausalRelation{Contains, Calls, Reads, Writes, Publishes, Consumes, CausalDependsOn, Tests, RuntimeCalls, Causes}
}

// EntityRef is local to an immutable source revision. No implicit branch/latest
// resolution occurs in core or SQL. Trusted collection must pin source identities.
type EntityRef struct {
	RepositoryID string `json:"repository_id"`
	Revision     string `json:"revision"`
	EntityID     string `json:"entity_id"`
}

func (r EntityRef) Validate() error {
	if !tenant.ValidID(r.RepositoryID) || !evidence.ValidIdentifier(r.Revision) || !evidence.ValidIdentifier(r.EntityID) {
		return ErrInvalidGraph
	}
	return nil
}

type RevisionPin struct {
	RepositoryID string `json:"repository_id"`
	Revision     string `json:"revision"`
}

// RevisionScope reuses the exact same admitted repository/retention scope as
// EvidenceGraph. Exactly one pin per repository prevents mixed-revision joins.
type RevisionScope struct {
	Access    Scope
	Revisions []RevisionPin
}

func (s RevisionScope) Validate() error {
	if s.Access.Validate() != nil || len(s.Revisions) != len(s.Access.RepositoryIDs) {
		return ErrInvalidGraph
	}
	seen := map[string]bool{}
	for _, pin := range s.Revisions {
		if !slices.Contains(s.Access.RepositoryIDs, pin.RepositoryID) || !evidence.ValidIdentifier(pin.Revision) || seen[pin.RepositoryID] {
			return ErrInvalidGraph
		}
		seen[pin.RepositoryID] = true
	}
	return nil
}
func (s RevisionScope) Includes(ref EntityRef, created time.Time) bool {
	return s.Access.Contains(ref.RepositoryID, created) && slices.Contains(s.Revisions, RevisionPin{ref.RepositoryID, ref.Revision})
}

type ChangeNodeFields struct {
	Schema    string          `json:"schema"`
	TenantID  string          `json:"tenant_id"`
	Ref       EntityRef       `json:"ref"`
	Kind      EntityKind      `json:"kind"`
	Name      string          `json:"name"`
	Path      string          `json:"path"`
	InputHash string          `json:"input_hash"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}
type ChangeNode struct{ canonical string }

func NewChangeNode(f ChangeNodeFields) (ChangeNode, error) {
	if f.Schema != ChangeNodeSchema || !tenant.ValidID(f.TenantID) || f.Ref.Validate() != nil || !slices.Contains(EntityKinds(), f.Kind) || !evidence.ValidText(f.Name, 1024) || strings.TrimSpace(f.Name) == "" || !sha256Hash(f.InputHash) || !timestamp(f.CreatedAt) || f.Path != "" && !evidence.ValidPath(f.Path) || f.Kind == FileEntity && f.Path == "" {
		return ChangeNode{}, ErrInvalidGraph
	}
	f.CreatedAt = f.CreatedAt.UTC().Truncate(time.Microsecond)
	raw, err := jsonobject.Normalize(f.Payload, MaxChangeObjectBytes/2)
	if err != nil {
		return ChangeNode{}, ErrInvalidGraph
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var payload any
	if d.Decode(&payload) != nil {
		return ChangeNode{}, ErrInvalidGraph
	}
	f.Payload, err = json.Marshal(payload)
	if err != nil {
		return ChangeNode{}, ErrInvalidGraph
	}
	raw, err = json.Marshal(f)
	if err != nil || len(raw) > MaxChangeObjectBytes {
		return ChangeNode{}, ErrInvalidGraph
	}
	return ChangeNode{string(raw)}, nil
}
func (n ChangeNode) Fields() (ChangeNodeFields, error) {
	var f ChangeNodeFields
	if n.canonical == "" || json.Unmarshal([]byte(n.canonical), &f) != nil {
		return f, ErrInvalidGraph
	}
	return f, nil
}
func (n ChangeNode) MarshalJSON() ([]byte, error) {
	if n.canonical == "" {
		return nil, ErrInvalidGraph
	}
	return []byte(n.canonical), nil
}
func (*ChangeNode) UnmarshalJSON([]byte) error { return ErrInvalidGraph }
func (n ChangeNode) ContentHash() string {
	if n.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(ChangeNodeSchema + "\n" + n.canonical))
}

type CausalBasis string

const (
	BasisObserved CausalBasis = "OBSERVED"
	BasisInferred CausalBasis = "INFERRED"
	BasisUnknown  CausalBasis = "UNKNOWN"
)

// A directed link is an attributed assertion backed by an existing immutable
// Evidence record. Even OBSERVED/CAUSES is not a proof of causality or PASS.
type ChangeLinkFields struct {
	Schema     string         `json:"schema"`
	TenantID   string         `json:"tenant_id"`
	LinkID     string         `json:"link_id"`
	From       EntityRef      `json:"from"`
	To         EntityRef      `json:"to"`
	Relation   CausalRelation `json:"relation"`
	Basis      CausalBasis    `json:"basis"`
	EvidenceID string         `json:"evidence_id"`
	CreatedAt  time.Time      `json:"created_at"`
}
type ChangeLink struct{ canonical string }

func NewChangeLink(f ChangeLinkFields) (ChangeLink, error) {
	if f.Schema != ChangeLinkSchema || !tenant.ValidID(f.TenantID) || !evidence.ValidIdentifier(f.LinkID) || f.From.Validate() != nil || f.To.Validate() != nil || f.From == f.To || !slices.Contains(CausalRelations(), f.Relation) || !slices.Contains([]CausalBasis{BasisObserved, BasisInferred, BasisUnknown}, f.Basis) || !evidence.ValidIdentifier(f.EvidenceID) || !timestamp(f.CreatedAt) || f.From.RepositoryID == f.To.RepositoryID && f.From.Revision != f.To.Revision {
		return ChangeLink{}, ErrInvalidGraph
	}
	f.CreatedAt = f.CreatedAt.UTC().Truncate(time.Microsecond)
	raw, err := json.Marshal(f)
	if err != nil {
		return ChangeLink{}, ErrInvalidGraph
	}
	return ChangeLink{string(raw)}, nil
}
func (e ChangeLink) Fields() (ChangeLinkFields, error) {
	var f ChangeLinkFields
	if e.canonical == "" || json.Unmarshal([]byte(e.canonical), &f) != nil {
		return f, ErrInvalidGraph
	}
	return f, nil
}
func (e ChangeLink) MarshalJSON() ([]byte, error) {
	if e.canonical == "" {
		return nil, ErrInvalidGraph
	}
	return []byte(e.canonical), nil
}
func (*ChangeLink) UnmarshalJSON([]byte) error { return ErrInvalidGraph }
func (e ChangeLink) ContentHash() string {
	if e.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(ChangeLinkSchema + "\n" + e.canonical))
}

func DecodeChangeNode(raw []byte) (ChangeNode, error) {
	raw, err := jsonobject.Normalize(raw, MaxChangeObjectBytes)
	if err != nil {
		return ChangeNode{}, ErrGraphData
	}
	f, err := changeObject(raw, "schema", "tenant_id", "ref", "kind", "name", "path", "input_hash", "payload", "created_at")
	if err != nil {
		return ChangeNode{}, err
	}
	if _, err := changeObject(f["ref"], "repository_id", "revision", "entity_id"); err != nil {
		return ChangeNode{}, err
	}
	var fields ChangeNodeFields
	if json.Unmarshal(raw, &fields) != nil {
		return ChangeNode{}, ErrGraphData
	}
	return NewChangeNode(fields)
}
func DecodeChangeLink(raw []byte) (ChangeLink, error) {
	raw, err := jsonobject.Normalize(raw, MaxChangeObjectBytes)
	if err != nil {
		return ChangeLink{}, ErrGraphData
	}
	f, err := changeObject(raw, "schema", "tenant_id", "link_id", "from", "to", "relation", "basis", "evidence_id", "created_at")
	if err != nil {
		return ChangeLink{}, err
	}
	for _, key := range []string{"from", "to"} {
		if _, err := changeObject(f[key], "repository_id", "revision", "entity_id"); err != nil {
			return ChangeLink{}, err
		}
	}
	var fields ChangeLinkFields
	if json.Unmarshal(raw, &fields) != nil {
		return ChangeLink{}, ErrGraphData
	}
	return NewChangeLink(fields)
}
func changeObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	var f map[string]json.RawMessage
	if json.Unmarshal(raw, &f) != nil || len(f) != len(keys) {
		return nil, ErrGraphData
	}
	for _, key := range keys {
		v, ok := f[key]
		if !ok || bytes.Equal(v, []byte("null")) {
			return nil, ErrGraphData
		}
	}
	return f, nil
}
func sha256Hash(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func timestamp(t time.Time) bool {
	return !t.IsZero() && validTime(t) && !t.UTC().Truncate(time.Microsecond).IsZero()
}

type ChangeQuery struct {
	Root        EntityRef
	Direction   Direction
	Relation    CausalRelation
	AfterLinkID string
	Limit       int
}

func (q ChangeQuery) Validate() error {
	if q.Root.Validate() != nil || !slices.Contains([]Direction{Outgoing, Incoming, Both}, q.Direction) || q.Relation != "" && !slices.Contains(CausalRelations(), q.Relation) || q.AfterLinkID != "" && !evidence.ValidIdentifier(q.AfterLinkID) || q.Limit < 1 || q.Limit > MaxPageSize {
		return ErrInvalidGraph
	}
	return nil
}

type ChangePage struct {
	Links      []ChangeLink `json:"links"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// ChangeGraph is the causal/revision API on the same PostgreSQL graph adapter.
// Every endpoint and evidence source must be visible in admitted scope. History
// is append-only; pin selection, not plan-specific code, selects a graph view.
type ChangeGraph interface {
	PutChangeNode(context.Context, RevisionScope, ChangeNode) error
	GetChangeNode(context.Context, RevisionScope, EntityRef) (ChangeNode, error)
	PutChangeLink(context.Context, RevisionScope, ChangeLink) error
	ChangeNeighbors(context.Context, RevisionScope, ChangeQuery) (ChangePage, error)
}
