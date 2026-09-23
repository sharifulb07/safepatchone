package sir

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
)

const (
	Schema      = "safepatch.sir.v1"
	MaxBytes    = 2 * 1024 * 1024
	MaxNodes    = 4096
	MaxFiles    = 1024
	MaxMappings = 512
)

var (
	ErrInvalidRepresentation   = errors.New("invalid semantic intermediate representation")
	ErrImmutableRepresentation = errors.New("decode immutable SIR using Decode")
	ErrInvalidMapping          = errors.New("invalid SIR concept mapping")
	ErrUnknownConcept          = errors.New("unmapped language-specific concept")
	token                      = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	version                    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
)

type Kind string

const (
	Function             Kind = "function"
	Method               Kind = "method"
	Class                Kind = "class"
	Type                 Kind = "type"
	API                  Kind = "api"
	Dependency           Kind = "dependency"
	DBOperation          Kind = "db_operation"
	NetworkOperation     Kind = "network_operation"
	FilesystemOperation  Kind = "filesystem_operation"
	ProcessOperation     Kind = "process_operation"
	ConcurrencyOperation Kind = "concurrency_operation"
	Test                 Kind = "test"
	SideEffect           Kind = "side_effect"
)

func Kinds() []Kind {
	return []Kind{Function, Method, Class, Type, API, Dependency, DBOperation, NetworkOperation, FilesystemOperation, ProcessOperation, ConcurrencyOperation, Test, SideEffect}
}
func (k Kind) Valid() bool { return slices.Contains(Kinds(), k) }
func (k Kind) operation() bool {
	return slices.Contains([]Kind{DBOperation, NetworkOperation, FilesystemOperation, ProcessOperation, ConcurrencyOperation, SideEffect}, k)
}

type Coverage string

const (
	CoverageUnknown  Coverage = "UNKNOWN"
	CoveragePartial  Coverage = "PARTIAL"
	CoverageComplete Coverage = "COMPLETE"
)

// Metadata is supplied by trusted orchestration and the admitted extractor.
// Coverage/determinism describe extraction, never policy compliance or PASS.
type Metadata struct {
	Schema          string           `json:"schema"`
	TenantID        string           `json:"tenant_id"`
	Subject         evidence.Subject `json:"subject"`
	Producer        string           `json:"producer"`
	ProducerVersion string           `json:"producer_version"`
	Deterministic   bool             `json:"deterministic"`
	Coverage        Coverage         `json:"coverage"`
}
type SourceFile struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
}

// Concept preserves adapter provenance alongside normalized semantics. NativeKind
// is an adapter token, not syntax to execute. Target is an inert external symbol
// or resource description; TargetID references a local node when resolved.
// Empty Target/TargetID means unresolved, never "no side effects".
type Concept struct {
	ID         string                  `json:"id"`
	Language   string                  `json:"language"`
	NativeKind string                  `json:"native_kind"`
	Name       string                  `json:"name"`
	Source     evidence.SourceLocation `json:"source"`
	ParentID   string                  `json:"parent_id"`
	TargetID   string                  `json:"target_id"`
	Target     string                  `json:"target"`
	Action     string                  `json:"action"`
}
type Node struct {
	Concept
	Kind Kind `json:"kind"`
}
type Fields struct {
	Metadata
	Files []SourceFile `json:"files"`
	Nodes []Node       `json:"nodes"`
}

// Representation owns private canonical bytes. IDs are local to this snapshot;
// references cannot silently resolve across repositories, revisions or tenants.
type Representation struct{ canonical string }

func (r Representation) Fields() (Fields, error) {
	var f Fields
	if r.canonical == "" || json.Unmarshal([]byte(r.canonical), &f) != nil {
		return f, ErrInvalidRepresentation
	}
	return f, nil
}
func (r Representation) MarshalJSON() ([]byte, error) {
	if r.canonical == "" {
		return nil, ErrInvalidRepresentation
	}
	return []byte(r.canonical), nil
}
func (*Representation) UnmarshalJSON([]byte) error { return ErrImmutableRepresentation }
func (r Representation) ContentHash() string {
	if r.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(Schema + "\n" + r.canonical))
}

// New seals already-normalized facts. It validates shape and references, not the
// semantic truth of an extractor's claims. NewNormalizer handles native kinds.
func New(ctx context.Context, f Fields) (Representation, error) {
	if err := checkContext(ctx); err != nil {
		return Representation{}, err
	}
	if f.Schema != Schema || !tenant.ValidID(f.TenantID) || f.Subject.Validate() != nil || !evidence.ValidIdentifier(f.Producer) || len(f.ProducerVersion) > 128 || !version.MatchString(f.ProducerVersion) || !slices.Contains([]Coverage{CoverageUnknown, CoveragePartial, CoverageComplete}, f.Coverage) || len(f.Files) > MaxFiles || len(f.Nodes) > MaxNodes {
		return Representation{}, ErrInvalidRepresentation
	}
	f.Files = append([]SourceFile{}, f.Files...)
	f.Nodes = append([]Node{}, f.Nodes...)
	slices.SortFunc(f.Files, func(a, b SourceFile) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(f.Nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	files := map[string]bool{}
	for _, file := range f.Files {
		if !safePath(file.Path) || !digest(file.ContentHash) || files[file.Path] {
			return Representation{}, ErrInvalidRepresentation
		}
		files[file.Path] = true
	}
	nodes := map[string]Node{}
	for _, node := range f.Nodes {
		if err := checkContext(ctx); err != nil {
			return Representation{}, err
		}
		if !validNode(node) || !files[node.Source.Path] {
			return Representation{}, ErrInvalidRepresentation
		}
		if _, exists := nodes[node.ID]; exists {
			return Representation{}, ErrInvalidRepresentation
		}
		nodes[node.ID] = node
	}
	for _, node := range f.Nodes {
		if node.TargetID != "" {
			if _, ok := nodes[node.TargetID]; !ok {
				return Representation{}, ErrInvalidRepresentation
			}
		}
		if node.ParentID != "" {
			parent, ok := nodes[node.ParentID]
			if !ok || !slices.Contains([]Kind{Function, Method, Class, Type, API, Test}, parent.Kind) {
				return Representation{}, ErrInvalidRepresentation
			}
		}
	}
	// Iterative linear containment cycle detection. Target cycles are legitimate
	// (recursive calls/dependencies); containment cycles are malformed input.
	state := map[string]uint8{}
	for _, node := range f.Nodes {
		if err := checkContext(ctx); err != nil {
			return Representation{}, err
		}
		chain := []string{}
		for id := node.ID; id != "" && state[id] != 2; id = nodes[id].ParentID {
			if state[id] == 1 {
				return Representation{}, ErrInvalidRepresentation
			}
			state[id] = 1
			chain = append(chain, id)
		}
		for _, id := range chain {
			state[id] = 2
		}
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxBytes {
		return Representation{}, ErrInvalidRepresentation
	}
	if err := checkContext(ctx); err != nil {
		return Representation{}, err
	}
	return Representation{canonical: string(raw)}, nil
}

func validNode(n Node) bool {
	if !n.Kind.Valid() || !evidence.ValidIdentifier(n.ID) || !validToken(n.Language) || !validToken(n.NativeKind) || !evidence.ValidText(n.Name, 1024) || strings.TrimSpace(n.Name) == "" || n.ParentID != "" && !evidence.ValidIdentifier(n.ParentID) || n.TargetID != "" && !evidence.ValidIdentifier(n.TargetID) || n.Target != "" && !evidence.ValidText(n.Target, 2048) {
		return false
	}
	if n.Kind.operation() {
		if !validToken(n.Action) {
			return false
		}
	} else if n.Action != "" {
		return false
	}
	s := n.Source
	return safePath(s.Path) && s.StartLine >= 0 && s.StartColumn >= 0 && s.EndLine >= 0 && s.EndColumn >= 0 && !(s.StartColumn > 0 && s.StartLine == 0) && !(s.EndLine > 0 && (s.StartLine == 0 || s.EndLine < s.StartLine)) && !(s.EndColumn > 0 && s.EndLine == 0) && !(s.EndLine == s.StartLine && s.EndColumn > 0 && (s.StartColumn == 0 || s.EndColumn < s.StartColumn))
}
func safePath(p string) bool {
	return len(p) <= 512 && strings.TrimSpace(p) == p && evidence.ValidPath(p)
}
func validToken(s string) bool { return len(s) <= 64 && token.MatchString(s) }
func digest(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func checkContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidRepresentation
	}
	return ctx.Err()
}
