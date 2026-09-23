package intent

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
)

const (
	ContractSchema    = "safepatch.intent.v1"
	MaxContractBytes  = 256 * 1024
	MaxGoalBytes      = 8192
	MaxStatementBytes = 4096
	MaxComponentBytes = 512
	MaxItemsPerField  = 256
	MaxTotalItems     = 2048
)

var (
	ErrInvalidContract     = errors.New("invalid intent contract")
	ErrConflictingContract = errors.New("intent contract contains conflicting declarations")
	ErrImmutableContract   = errors.New("decode immutable intent using DecodeIntentContract")
)

// IntentContractFields is an owned construction/inspection DTO. Lists are sets
// of literal statements or repository-relative component identifiers, not code.
// Every capability uses all twelve fields; no field is removed by commercial tier.
type IntentContractFields struct {
	Goal                string   `json:"goal"`
	Requirements        []string `json:"requirements"`
	AcceptanceCriteria  []string `json:"acceptance_criteria"`
	RequiredBehaviors   []string `json:"required_behaviors"`
	ForbiddenBehaviors  []string `json:"forbidden_behaviors"`
	AllowedComponents   []string `json:"allowed_components"`
	ForbiddenComponents []string `json:"forbidden_components"`
	ProtectedProperties []string `json:"protected_properties"`
	SecurityConstraints []string `json:"security_constraints"`
	DatabaseConstraints []string `json:"database_constraints"`
	APIConstraints      []string `json:"api_constraints"`
	BusinessConstraints []string `json:"business_constraints"`
}

// IntentContract owns immutable canonical bytes. It is portable change intent,
// not an assessment, tenant identity, permission, executable policy or approval.
// Storage/orchestration must bind it to an independently authorized change/run.
type IntentContract struct{ canonical string }

func NewIntentContract(fields IntentContractFields) (IntentContract, error) {
	f, err := canonicalFields(fields)
	if err != nil {
		return IntentContract{}, err
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxContractBytes {
		return IntentContract{}, ErrInvalidContract
	}
	return IntentContract{canonical: string(raw)}, nil
}
func (c IntentContract) Fields() (IntentContractFields, error) {
	var f IntentContractFields
	if c.canonical == "" || json.Unmarshal([]byte(c.canonical), &f) != nil {
		return f, ErrInvalidContract
	}
	return f, nil
}
func (c IntentContract) CanonicalJSON() ([]byte, error) {
	if c.canonical == "" {
		return nil, ErrInvalidContract
	}
	return []byte(c.canonical), nil
}
func (c IntentContract) MarshalJSON() ([]byte, error) { return c.CanonicalJSON() }
func (*IntentContract) UnmarshalJSON([]byte) error    { return ErrImmutableContract }

// ContentHash binds the canonical schema tag and all fields, never authorization.
func (c IntentContract) ContentHash() (string, error) {
	if c.canonical == "" {
		return "", ErrInvalidContract
	}
	return evidence.Digest([]byte(ContractSchema + "\n" + c.canonical)), nil
}

func canonicalFields(f IntentContractFields) (IntentContractFields, error) {
	if len(f.Goal) > MaxGoalBytes {
		return IntentContractFields{}, ErrInvalidContract
	}
	f.Goal = strings.ReplaceAll(f.Goal, "\r\n", "\n")
	if !validStatement(f.Goal, MaxGoalBytes) {
		return IntentContractFields{}, ErrInvalidContract
	}
	totalItems, totalBytes := 0, len(f.Goal)
	for _, field := range []struct {
		values     *[]string
		components bool
	}{
		{&f.Requirements, false}, {&f.AcceptanceCriteria, false}, {&f.RequiredBehaviors, false}, {&f.ForbiddenBehaviors, false},
		{&f.AllowedComponents, true}, {&f.ForbiddenComponents, true}, {&f.ProtectedProperties, false},
		{&f.SecurityConstraints, false}, {&f.DatabaseConstraints, false}, {&f.APIConstraints, false}, {&f.BusinessConstraints, false},
	} {
		values := *field.values
		totalItems += len(values)
		if len(values) > MaxItemsPerField || totalItems > MaxTotalItems {
			return IntentContractFields{}, ErrInvalidContract
		}
		out := make([]string, 0, len(values))
		for _, s := range values {
			if field.components {
				if len(s) > MaxComponentBytes || strings.TrimSpace(s) != s || !evidence.ValidPath(s) {
					return IntentContractFields{}, ErrInvalidContract
				}
			} else {
				if len(s) > MaxStatementBytes {
					return IntentContractFields{}, ErrInvalidContract
				}
				s = strings.ReplaceAll(s, "\r\n", "\n")
				if !validStatement(s, MaxStatementBytes) {
					return IntentContractFields{}, ErrInvalidContract
				}
			}
			totalBytes += len(s)
			if totalBytes > MaxContractBytes {
				return IntentContractFields{}, ErrInvalidContract
			}
			out = append(out, s)
		}
		slices.Sort(out)
		*field.values = slices.Compact(out)
	}
	if overlaps(f.RequiredBehaviors, f.ForbiddenBehaviors) || overlaps(f.AllowedComponents, f.ForbiddenComponents) {
		return IntentContractFields{}, ErrConflictingContract
	}
	return f, nil
}
func validStatement(s string, bound int) bool {
	if len(s) > bound || strings.TrimSpace(s) == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.In(r, unicode.Cf) || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}
func overlaps(a, b []string) bool {
	for _, s := range a {
		if _, ok := slices.BinarySearch(b, s); ok {
			return true
		}
	}
	return false
}

// DecodeIntentContract accepts the complete twelve-field wire schema only.
// Duplicate keys, nulls, aliases, unknown fields and malformed text fail closed.
// A missing field is not silently interpreted as an empty constraint set.
func DecodeIntentContract(raw []byte) (IntentContract, error) {
	checked, err := jsonobject.Normalize(raw, MaxContractBytes)
	if err != nil {
		return IntentContract{}, ErrInvalidContract
	}
	keys := []string{"goal", "requirements", "acceptance_criteria", "required_behaviors", "forbidden_behaviors", "allowed_components", "forbidden_components", "protected_properties", "security_constraints", "database_constraints", "api_constraints", "business_constraints"}
	var object map[string]json.RawMessage
	if json.Unmarshal(checked, &object) != nil || len(object) != len(keys) {
		return IntentContract{}, ErrInvalidContract
	}
	for key, value := range object {
		if !slices.Contains(keys, key) || bytes.Equal(value, []byte("null")) {
			return IntentContract{}, ErrInvalidContract
		}
	}
	var f IntentContractFields
	if json.Unmarshal(checked, &f) != nil {
		return IntentContract{}, ErrInvalidContract
	}
	return NewIntentContract(f)
}
