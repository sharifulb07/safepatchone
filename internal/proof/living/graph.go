// Package living maintains proof relevance without edition-dependent execution.
package living

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"safepatch/internal/domain"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
)

const GraphSchema = "safepatch.proof-dependency-graph.v1"
const Schema = "safepatch.living-proof.v1"
const MaxBytes = 1024 * 1024
const MaxNodes = 1024
const MaxEdges = 4096

var ErrInvalid = errors.New("invalid living proof input")
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Kind string

const (
	Input Kind = "input"
	Proof Kind = "proof"
)

// Dependencies point from a dependent to its prerequisites. Digest pins an
// immutable artifact or completed proof; IDs are opaque, never URLs or paths.
// Status is the original verification result, never a mutable freshness flag.
type Node struct {
	ID           string          `json:"id"`
	Kind         Kind            `json:"kind"`
	Digest       string          `json:"digest"`
	Dependencies []string        `json:"dependencies"`
	Status       evidence.Status `json:"status"`
	RecordedAt   time.Time       `json:"recorded_at"`
	ValidUntil   time.Time       `json:"valid_until"`
}

type GraphFields struct {
	Schema    string        `json:"schema"`
	ID        string        `json:"id"`
	Change    domain.Change `json:"change"`
	CreatedAt time.Time     `json:"created_at"`
	Nodes     []Node        `json:"nodes"`
}

// ProofDependencyGraph owns canonical bytes; callers cannot mutate history.
// Construction validates structure, not authority/completeness of supplied pins.
type ProofDependencyGraph struct{ canonical string }

func NewGraph(f GraphFields) (ProofDependencyGraph, error) {
	bad := ProofDependencyGraph{}
	if f.Schema != GraphSchema || !evidence.ValidIdentifier(f.ID) || f.Change.Validate() != nil || !validTime(f.CreatedAt) || len(f.Nodes) == 0 || len(f.Nodes) > MaxNodes {
		return bad, ErrInvalid
	}
	f.CreatedAt = f.CreatedAt.UTC()
	f.Nodes = slices.Clone(f.Nodes)
	seen := map[string]bool{}
	proofs, edges := 0, 0
	for i, n := range f.Nodes {
		if !evidence.ValidIdentifier(n.ID) || seen[n.ID] || !validDigest(n.Digest) || !validTime(n.RecordedAt) || !validTime(n.ValidUntil) || n.RecordedAt.After(f.CreatedAt) || !n.ValidUntil.After(n.RecordedAt) {
			return bad, ErrInvalid
		}
		seen[n.ID] = true
		switch n.Kind {
		case Input:
			if len(n.Dependencies) != 0 || n.Status != evidence.StatusUnknown {
				return bad, ErrInvalid
			}
		case Proof:
			proofs++
			if len(n.Dependencies) == 0 || (n.Status != evidence.StatusPass && n.Status != evidence.StatusFail && n.Status != evidence.StatusUnknown) {
				return bad, ErrInvalid
			}
		default:
			return bad, ErrInvalid
		}
		edges += len(n.Dependencies)
		if edges > MaxEdges {
			return bad, ErrInvalid
		}
		n.Dependencies = append([]string{}, n.Dependencies...)
		slices.Sort(n.Dependencies)
		for j, dep := range n.Dependencies {
			if !evidence.ValidIdentifier(dep) || dep == n.ID || (j > 0 && n.Dependencies[j-1] == dep) {
				return bad, ErrInvalid
			}
		}
		n.RecordedAt, n.ValidUntil = n.RecordedAt.UTC(), n.ValidUntil.UTC()
		f.Nodes[i] = n
	}
	if proofs == 0 {
		return bad, ErrInvalid
	}
	slices.SortFunc(f.Nodes, func(a, b Node) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if _, err := ordered(f.Nodes); err != nil {
		return bad, err
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxBytes {
		return bad, ErrInvalid
	}
	return ProofDependencyGraph{string(raw)}, nil
}

// Kahn traversal bounds work by nodes + edges and never recurses on input.
func ordered(nodes []Node) ([]Node, error) {
	byID := map[string]Node{}
	remaining := map[string]int{}
	dependents := map[string][]string{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	queue := []string{}
	for _, n := range nodes {
		remaining[n.ID] = len(n.Dependencies)
		if len(n.Dependencies) == 0 {
			queue = append(queue, n.ID)
		}
		for _, dep := range n.Dependencies {
			if _, ok := byID[dep]; !ok {
				return nil, ErrInvalid
			}
			dependents[dep] = append(dependents[dep], n.ID)
		}
	}
	result := make([]Node, 0, len(nodes))
	for head := 0; head < len(queue); head++ {
		id := queue[head]
		result = append(result, byID[id])
		for _, dependent := range dependents[id] {
			remaining[dependent]--
			if remaining[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}
	if len(result) != len(nodes) {
		return nil, ErrInvalid
	}
	return result, nil
}

func (g ProofDependencyGraph) Fields() (GraphFields, error) {
	var f GraphFields
	if g.canonical == "" || json.Unmarshal([]byte(g.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (g ProofDependencyGraph) MarshalJSON() ([]byte, error) {
	if g.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(g.canonical), nil
}
func (g ProofDependencyGraph) Digest() string {
	if g.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(g.canonical))
}
func DecodeGraph(raw []byte) (ProofDependencyGraph, error) {
	checked, err := jsonobject.Normalize(raw, MaxBytes)
	if err != nil {
		return ProofDependencyGraph{}, ErrInvalid
	}
	var f GraphFields
	if json.Unmarshal(checked, &f) != nil {
		return ProofDependencyGraph{}, ErrInvalid
	}
	g, err := NewGraph(f)
	if err != nil || !sameJSON(checked, []byte(g.canonical)) {
		return ProofDependencyGraph{}, ErrInvalid
	}
	return g, nil
}
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	a, _ = json.Marshal(x)
	b, _ = json.Marshal(y)
	return bytes.Equal(a, b)
}
func validDigest(d string) bool  { return len(d) == 64 && digestPattern.MatchString(d) }
func validTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }
