package sir

import "context"

// Mapping is trusted adapter configuration. For example, a Go adapter maps
// func_decl to function and receiver_method to method; a TypeScript adapter can
// map function_declaration to the same function kind. There is no language guess
// based on a filename and no interpretation of source text in this registry.
type Mapping struct {
	Language   string
	NativeKind string
	Kind       Kind
}
type Normalizer struct{ mappings map[[2]string]Kind }
type Extraction struct {
	Metadata
	Files    []SourceFile
	Concepts []Concept
}

func NewNormalizer(mappings []Mapping) (*Normalizer, error) {
	if len(mappings) == 0 || len(mappings) > MaxMappings {
		return nil, ErrInvalidMapping
	}
	n := &Normalizer{mappings: map[[2]string]Kind{}}
	for _, m := range mappings {
		key := [2]string{m.Language, m.NativeKind}
		if !validToken(m.Language) || !validToken(m.NativeKind) || !m.Kind.Valid() {
			return nil, ErrInvalidMapping
		}
		if _, exists := n.mappings[key]; exists {
			return nil, ErrInvalidMapping
		}
		n.mappings[key] = m.Kind
	}
	return n, nil
}

// Normalize is all-or-error: unmapped concepts are never dropped or coerced to
// a function/type. Coverage UNKNOWN/PARTIAL is preserved, not upgraded. Creating
// a valid SIR cannot establish verification PASS or grant production authority.
func (n *Normalizer) Normalize(ctx context.Context, in Extraction) (Representation, error) {
	if err := checkContext(ctx); err != nil {
		return Representation{}, err
	}
	if n == nil || len(n.mappings) == 0 {
		return Representation{}, ErrInvalidMapping
	}
	if len(in.Concepts) > MaxNodes || len(in.Files) > MaxFiles {
		return Representation{}, ErrInvalidRepresentation
	}
	f := Fields{Metadata: in.Metadata, Files: in.Files, Nodes: make([]Node, 0, len(in.Concepts))}
	for _, c := range in.Concepts {
		if err := checkContext(ctx); err != nil {
			return Representation{}, err
		}
		if !validToken(c.Language) || !validToken(c.NativeKind) {
			return Representation{}, ErrInvalidRepresentation
		}
		kind, ok := n.mappings[[2]string{c.Language, c.NativeKind}]
		if !ok {
			return Representation{}, ErrUnknownConcept
		}
		f.Nodes = append(f.Nodes, Node{Concept: c, Kind: kind})
	}
	return New(ctx, f)
}
