package living

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
)

const MaxSnapshotAge = time.Minute

type State string

const (
	Current State = "CURRENT"
	Stale   State = "STALE"
	Unknown State = "UNKNOWN"
	Failed  State = "FAILED"
	Expired State = "EXPIRED"
)

type Observation struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Digest    string `json:"digest"`
}

// Snapshot must come from authoritative dependency state. Missing observations
// mean UNKNOWN. GraphDigest binds even empty snapshots to one tenant/change.
type Snapshot struct {
	GraphDigest string        `json:"graph_digest"`
	ObservedAt  time.Time     `json:"observed_at"`
	ValidUntil  time.Time     `json:"valid_until"`
	Nodes       []Observation `json:"nodes"`
}
type ProofState struct {
	ID      string   `json:"id"`
	State   State    `json:"state"`
	Reasons []string `json:"reasons"`
}
type Fields struct {
	Schema              string       `json:"schema"`
	GraphDigest         string       `json:"graph_digest"`
	Snapshot            Snapshot     `json:"snapshot"`
	EvaluatedAt         time.Time    `json:"evaluated_at"`
	ValidUntil          time.Time    `json:"valid_until"`
	State               State        `json:"state"`
	Proofs              []ProofState `json:"proofs"`
	NeedsReverification []string     `json:"needs_reverification"`
}
type LivingProof struct{ canonical string }

// Evaluate never runs repository code and never turns freshness into production
// approval. Reverification must use the existing verifier and policy guards.
func Evaluate(ctx context.Context, g ProofDependencyGraph, snapshot Snapshot, now time.Time) (LivingProof, error) {
	bad := LivingProof{}
	if ctx == nil {
		return bad, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return bad, err
	}
	f, err := g.Fields()
	if err != nil || !validTime(now) || now.Before(f.CreatedAt) || snapshot.GraphDigest != g.Digest() || !validTime(snapshot.ObservedAt) || !validTime(snapshot.ValidUntil) || snapshot.ObservedAt.Before(f.CreatedAt) || snapshot.ObservedAt.After(now) || !now.Before(snapshot.ValidUntil) || snapshot.ValidUntil.Sub(snapshot.ObservedAt) > MaxSnapshotAge || len(snapshot.Nodes) > MaxNodes {
		return bad, ErrInvalid
	}
	now = now.UTC()
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	snapshot.ValidUntil = snapshot.ValidUntil.UTC()
	snapshot.Nodes = append([]Observation{}, snapshot.Nodes...)
	slices.SortFunc(snapshot.Nodes, func(a, b Observation) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	known := map[string]bool{}
	for _, n := range f.Nodes {
		known[n.ID] = true
	}
	observed := map[string]Observation{}
	for _, n := range snapshot.Nodes {
		if !known[n.ID] {
			return bad, ErrInvalid
		}
		if _, ok := observed[n.ID]; ok {
			return bad, ErrInvalid
		}
		if (n.Available && !validDigest(n.Digest)) || (!n.Available && n.Digest != "") {
			return bad, ErrInvalid
		}
		observed[n.ID] = n
	}
	nodes, err := ordered(f.Nodes)
	if err != nil {
		return bad, err
	}
	states := map[string]ProofState{}
	result := Fields{Schema: Schema, GraphDigest: g.Digest(), Snapshot: snapshot, EvaluatedAt: now, ValidUntil: snapshot.ValidUntil, State: Current, Proofs: []ProofState{}, NeedsReverification: []string{}}
	for _, n := range nodes {
		if err := ctx.Err(); err != nil {
			return bad, err
		}
		state := ProofState{ID: n.ID, State: Current, Reasons: []string{}}
		mark := func(s State, reason string) {
			if rank(s) > rank(state.State) {
				state.State = s
			}
			state.Reasons = append(state.Reasons, reason)
		}
		v, ok := observed[n.ID]
		if !ok || !v.Available {
			mark(Unknown, "unavailable")
		} else if v.Digest != n.Digest {
			mark(Stale, "digest_changed")
		}
		if !now.Before(n.ValidUntil) {
			mark(Expired, "expired")
		} else if n.ValidUntil.Before(result.ValidUntil) {
			result.ValidUntil = n.ValidUntil
		}
		if n.Kind == Proof {
			if n.Status == evidence.StatusUnknown {
				mark(Unknown, "verification_unknown")
			}
			if n.Status == evidence.StatusFail {
				mark(Failed, "verification_failed")
			}
		}
		for _, dep := range n.Dependencies {
			if d := states[dep]; d.State != Current {
				mark(d.State, "dependency:"+dep)
			}
		}
		slices.Sort(state.Reasons)
		states[n.ID] = state
		if n.Kind == Proof {
			result.Proofs = append(result.Proofs, state)
			if state.State != Current {
				result.NeedsReverification = append(result.NeedsReverification, n.ID)
			}
			if rank(state.State) > rank(result.State) {
				result.State = state.State
			}
		}
	}
	slices.SortFunc(result.Proofs, func(a, b ProofState) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	slices.Sort(result.NeedsReverification)
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > MaxBytes {
		return bad, ErrInvalid
	}
	return LivingProof{string(raw)}, nil
}
func rank(s State) int {
	switch s {
	case Current:
		return 0
	case Stale:
		return 1
	case Expired:
		return 2
	case Failed:
		return 3
	default:
		return 4
	}
}
func (p LivingProof) Fields() (Fields, error) {
	var f Fields
	if p.canonical == "" || json.Unmarshal([]byte(p.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (p LivingProof) MarshalJSON() ([]byte, error) {
	if p.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(p.canonical), nil
}
func (p LivingProof) Digest() string {
	if p.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(p.canonical))
}

// Decode recomputes the historical result, rejecting poisoned result fields.
// It does not claim that a stored evaluation remains current now.
func Decode(ctx context.Context, g ProofDependencyGraph, raw []byte) (LivingProof, error) {
	checked, err := jsonobject.Normalize(raw, MaxBytes)
	if err != nil {
		return LivingProof{}, ErrInvalid
	}
	var f Fields
	if json.Unmarshal(checked, &f) != nil {
		return LivingProof{}, ErrInvalid
	}
	p, err := Evaluate(ctx, g, f.Snapshot, f.EvaluatedAt)
	if err != nil || !sameJSON(checked, []byte(p.canonical)) {
		return LivingProof{}, ErrInvalid
	}
	return p, nil
}
