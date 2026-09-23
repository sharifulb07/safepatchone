package quorum

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/graph"
	"safepatch/internal/tenant"
)

type Decision string

const Satisfied Decision = "SATISFIED"
const NotProven Decision = "NOT_PROVEN"

// Input must contain the complete relevant retained run observations. Completeness
// and repository binding are established by the loader, not by uploaded JSON.
type Input struct {
	TenantID     string
	RepositoryID string
	ChangeID     string
	RunID        string
	AsOf         time.Time
	Complete     bool
	Evidence     []evidence.Evidence
	Edges        []graph.Edge
}
type Observation struct {
	EvidenceID string          `json:"evidence_id"`
	Digest     string          `json:"digest"`
	SourceID   string          `json:"source_id"`
	Family     Family          `json:"family"`
	Status     evidence.Status `json:"status"`
	Reason     string          `json:"reason"`
}
type RequirementResult struct {
	ID                 string        `json:"id"`
	RequiredFamilies   int           `json:"required_families"`
	SatisfiedFamilies  []Family      `json:"satisfied_families"`
	MissingFamilies    []Family      `json:"missing_families"`
	Deficit            int           `json:"deficit"`
	Decision           Decision      `json:"decision"`
	Observations       []Observation `json:"observations"`
	ContradictingEdges []string      `json:"contradicting_edges"`
}
type ResultFields struct {
	Schema       string              `json:"schema"`
	PolicyDigest string              `json:"policy_digest"`
	TenantID     string              `json:"tenant_id"`
	RepositoryID string              `json:"repository_id"`
	ChangeID     string              `json:"change_id"`
	RunID        string              `json:"run_id"`
	AsOf         time.Time           `json:"as_of"`
	Complete     bool                `json:"complete"`
	Decision     Decision            `json:"decision"`
	Status       evidence.Status     `json:"status"`
	Requirements []RequirementResult `json:"requirements"`
}
type QuorumResult struct{ canonical string }
type EvidenceQuorum struct{}

func (EvidenceQuorum) Evaluate(ctx context.Context, p QuorumPolicy, in Input) (QuorumResult, error) {
	bad := QuorumResult{}
	if ctx == nil {
		return bad, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return bad, err
	}
	f, err := p.Fields()
	if err != nil || !tenant.ValidID(in.TenantID) || !tenant.ValidID(in.RepositoryID) || !evidence.ValidIdentifier(in.ChangeID) || !evidence.ValidIdentifier(in.RunID) || in.AsOf.IsZero() || in.AsOf.Year() < 1 || in.AsOf.Year() > 9999 || len(in.Evidence) > MaxEvidence || len(in.Edges) > 512 {
		return bad, ErrInvalid
	}
	index := map[[2]string][]evidence.EvidenceFields{}
	nodes := map[string]evidence.EvidenceFields{}
	bytes := 0
	for _, node := range in.Evidence {
		if err := ctx.Err(); err != nil {
			return bad, err
		}
		raw, err := node.CanonicalJSON()
		bytes += len(raw)
		if err != nil || bytes > 16*1024*1024 {
			return bad, ErrInvalid
		}
		n, err := node.Fields()
		if err != nil || n.TenantID != in.TenantID || n.ChangeID != in.ChangeID || n.RunID != in.RunID || n.CreatedAt.After(in.AsOf) || nodes[n.EvidenceID].EvidenceID != "" {
			return bad, ErrInvalid
		}
		nodes[n.EvidenceID] = n
		key := [2]string{n.Producer, n.Type}
		index[key] = append(index[key], n)
	}
	edges := map[string]bool{}
	for _, e := range in.Edges {
		if e.Validate() != nil || e.TenantID != in.TenantID || e.CreatedAt.After(in.AsOf) || edges[e.EdgeID] || nodes[e.FromEvidenceID].EvidenceID == "" || nodes[e.ToEvidenceID].EvidenceID == "" {
			return bad, ErrInvalid
		}
		edges[e.EdgeID] = true
	}
	sources := map[string]Source{}
	for _, s := range f.Sources {
		sources[s.ID] = s
	}
	r := ResultFields{Schema: "safepatch.quorum-result.v1", PolicyDigest: p.Digest(), TenantID: in.TenantID, RepositoryID: in.RepositoryID, ChangeID: in.ChangeID, RunID: in.RunID, AsOf: in.AsOf.UTC(), Complete: in.Complete, Decision: Satisfied, Status: evidence.StatusPass, Requirements: []RequirementResult{}}
	for _, req := range f.Requirements {
		if err := ctx.Err(); err != nil {
			return bad, err
		}
		out := RequirementResult{ID: req.ID, RequiredFamilies: req.RequiredFamilies, SatisfiedFamilies: []Family{}, MissingFamilies: []Family{}, Decision: Satisfied, Observations: []Observation{}, ContradictingEdges: []string{}}
		familyPass, familyIncomplete := map[Family]bool{}, map[Family]bool{}
		relevant := map[string]bool{}
		observationSources := map[string]Source{}
		contradiction := false
		for _, id := range req.Sources {
			s := sources[id]
			if _, ok := familyPass[s.Family]; !ok {
				familyPass[s.Family] = false
			}
			for _, n := range index[[2]string{s.Producer, s.Type}] {
				relevant[n.EvidenceID] = true
				observationSources[n.EvidenceID] = s
				o := Observation{EvidenceID: n.EvidenceID, Digest: n.ContentHash, SourceID: id, Family: s.Family, Status: n.Status, Reason: "INCOMPLETE"}
				switch {
				case n.Status == evidence.StatusFail || n.Status == evidence.StatusWarn:
					contradiction = true
					familyIncomplete[s.Family] = true
					o.Reason = "CONTRADICTS"
				case n.Status == evidence.StatusPass && n.Confidence >= req.MinConfidence && (!req.Deterministic || n.Deterministic):
					familyPass[s.Family] = true
					o.Reason = "SUPPORTS"
				default:
					familyIncomplete[s.Family] = true
					if n.Status == evidence.StatusPass {
						o.Reason = "UNQUALIFIED"
					}
				}
				out.Observations = append(out.Observations, o)
			}
		}
		for _, e := range in.Edges {
			if (e.Relationship == graph.Contradicts || e.Relationship == graph.Invalidates) && (relevant[e.FromEvidenceID] || relevant[e.ToEvidenceID]) {
				contradiction = true
				out.ContradictingEdges = append(out.ContradictingEdges, e.EdgeID)
				// Preserve both endpoint IDs and hashes, even if the contradictor
				// is not itself an eligible proof source.
				for _, id := range []string{e.FromEvidenceID, e.ToEvidenceID} {
					n := nodes[id]
					s := observationSources[id]
					if s.ID != "" {
						familyIncomplete[s.Family] = true
					}
					out.Observations = append(out.Observations, Observation{EvidenceID: id, Digest: n.ContentHash, SourceID: s.ID, Family: s.Family, Status: n.Status, Reason: "GRAPH_CONTRADICTION"})
				}
			}
		}
		for family, pass := range familyPass {
			if pass && !familyIncomplete[family] {
				out.SatisfiedFamilies = append(out.SatisfiedFamilies, family)
			}
		}
		for _, family := range req.MandatoryFamilies {
			if !slices.Contains(out.SatisfiedFamilies, family) {
				out.MissingFamilies = append(out.MissingFamilies, family)
			}
		}
		out.Deficit = max(0, req.RequiredFamilies-len(out.SatisfiedFamilies))
		// For a count-only shortfall, list all remaining eligible families as
		// candidates; Deficit says exactly how many are still needed.
		if out.Deficit > 0 {
			for family := range familyPass {
				if !slices.Contains(out.SatisfiedFamilies, family) && !slices.Contains(out.MissingFamilies, family) {
					out.MissingFamilies = append(out.MissingFamilies, family)
				}
			}
		}
		if contradiction || !in.Complete || out.Deficit > 0 || len(out.MissingFamilies) > 0 {
			out.Decision = NotProven
			r.Decision = NotProven
			if r.Status != evidence.StatusFail {
				r.Status = evidence.StatusUnknown
			}
		}
		if contradiction {
			r.Status = evidence.StatusFail
		}
		slices.Sort(out.SatisfiedFamilies)
		slices.Sort(out.MissingFamilies)
		slices.Sort(out.ContradictingEdges)
		slices.SortFunc(out.Observations, func(a, b Observation) int {
			return strings.Compare(a.EvidenceID+":"+a.Reason, b.EvidenceID+":"+b.Reason)
		})
		out.Observations = slices.Compact(out.Observations)
		r.Requirements = append(r.Requirements, out)
	}
	if err := ctx.Err(); err != nil {
		return bad, err
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > evidence.MaxPayloadBytes {
		return bad, ErrInvalid
	}
	return QuorumResult{string(raw)}, nil
}

func (r QuorumResult) Fields() (ResultFields, error) {
	var f ResultFields
	if r.canonical == "" || json.Unmarshal([]byte(r.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (r QuorumResult) MarshalJSON() ([]byte, error) {
	if r.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(r.canonical), nil
}
func (*QuorumResult) UnmarshalJSON([]byte) error { return ErrInvalid }
func (r QuorumResult) Digest() string {
	if r.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(r.canonical))
}

// Evidence emits a canonical assessment with caller-supplied trusted intent
// binding. Deterministic describes the evaluator, not the underlying sources.
// FAIL means contradiction; UNKNOWN means insufficient proof. Neither is PASS.
func (r QuorumResult) Evidence(id, intentDigest string) (evidence.Evidence, error) {
	f, err := r.Fields()
	if err != nil {
		return evidence.Evidence{}, err
	}
	return evidence.NewEvidence(evidence.EvidenceFields{EvidenceID: id, TenantID: f.TenantID, ChangeID: f.ChangeID, RunID: f.RunID, Producer: "safepatch.quorum", ProducerVersion: "1.0.0", Category: "proof", Type: "proof.quorum", Deterministic: true, Status: f.Status, Severity: evidence.SeverityInfo, Confidence: 1, Summary: string(f.Decision), Payload: json.RawMessage(r.canonical), InputHashes: map[string]string{"intent": intentDigest, "quorum_policy": f.PolicyDigest, "quorum_result": r.Digest()}, CreatedAt: f.AsOf})
}

// Relationships links exact retained observations to the result for graph
// persistence. Caller must obtain graph write admission and store endpoints first.
func (r QuorumResult) Relationships(resultID string) ([]graph.Edge, error) {
	f, err := r.Fields()
	if err != nil || !evidence.ValidIdentifier(resultID) {
		return nil, ErrInvalid
	}
	out := []graph.Edge{}
	seen := map[string]bool{}
	for _, req := range f.Requirements {
		for _, o := range req.Observations {
			kind := graph.DerivedFrom
			if o.Reason == "SUPPORTS" {
				kind = graph.Supports
			}
			if o.Reason == "CONTRADICTS" || o.Reason == "GRAPH_CONTRADICTION" {
				kind = graph.Contradicts
			}
			from, to := o.EvidenceID, resultID
			if kind == graph.DerivedFrom {
				from, to = to, from
			}
			id := evidence.Digest([]byte(from + ":" + to + ":" + string(kind)))
			if seen[id] {
				continue
			}
			seen[id] = true
			e := graph.Edge{TenantID: f.TenantID, EdgeID: "quorum." + id, FromEvidenceID: from, ToEvidenceID: to, Relationship: kind, CreatedAt: f.AsOf}
			e, err = e.Canonical()
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b graph.Edge) int { return strings.Compare(a.EdgeID, b.EdgeID) })
	return out, nil
}
