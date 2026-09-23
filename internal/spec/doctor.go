package spec

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"safepatch/internal/evidence"
	"safepatch/internal/intent"
)

const DoctorVersion = "1.0.0"

var ErrInvalidInput = errors.New("invalid specification input")

// Assertion IDs bind a field and exact canonical statement, independent of
// array order. Goal is context, not an independently testable assertion.
type Assertion struct {
	ID        string `json:"id"`
	Field     string `json:"field"`
	Statement string `json:"statement"`
}

func Assertions(c intent.IntentContract) ([]Assertion, error) {
	f, err := c.Fields()
	if err != nil {
		return nil, err
	}
	out := []Assertion{}
	for _, group := range groups(f) {
		for _, statement := range group.values {
			out = append(out, Assertion{ID: evidence.Digest([]byte("safepatch.assertion.v1\n" + group.name + "\n" + statement)), Field: group.name, Statement: statement})
		}
	}
	return out, nil
}

type fieldGroup struct {
	name   string
	values []string
}

func groups(f intent.IntentContractFields) []fieldGroup {
	return []fieldGroup{
		{"requirements", f.Requirements}, {"acceptance_criteria", f.AcceptanceCriteria},
		{"required_behaviors", f.RequiredBehaviors}, {"forbidden_behaviors", f.ForbiddenBehaviors},
		{"allowed_components", f.AllowedComponents}, {"forbidden_components", f.ForbiddenComponents},
		{"protected_properties", f.ProtectedProperties}, {"security_constraints", f.SecurityConstraints},
		{"database_constraints", f.DatabaseConstraints}, {"api_constraints", f.APIConstraints},
		{"business_constraints", f.BusinessConstraints},
	}
}

type Validation struct {
	ProducerVersion string          `json:"producer_version"`
	ContractHash    string          `json:"contract_hash,omitempty"`
	Status          evidence.Status `json:"status"`
	Code            string          `json:"code"`
}

type SpecDoctor struct{}

// Validate checks explicit syntax, bounds and exact contradictions using the
// canonical contract decoder. PASS means a valid declaration, never verified code.
func (SpecDoctor) Validate(ctx context.Context, raw []byte) (Validation, error) {
	r := Validation{ProducerVersion: DoctorVersion, Status: evidence.StatusError, Code: "INVALID_INPUT"}
	if err := contextError(ctx); err != nil {
		return r, err
	}
	c, err := intent.DecodeIntentContract(raw)
	if err != nil {
		r.Status, r.Code = evidence.StatusFail, "INVALID_CONTRACT"
		if errors.Is(err, intent.ErrConflictingContract) {
			r.Code = "CONFLICTING_CONSTRAINTS"
		}
		return r, err
	}
	if err := contextError(ctx); err != nil {
		return r, err
	}
	r.ContractHash, _ = c.ContentHash()
	r.Status, r.Code = evidence.StatusPass, "VALID_EXPLICIT_CONSTRAINTS"
	return r, nil
}

type Suggestion struct {
	Code        string `json:"code"`
	Field       string `json:"field"`
	AssertionID string `json:"assertion_id,omitempty"`
	Message     string `json:"message"`
}

// Analysis is advisory. Score is the percentage of prose statements (including
// goal) containing at least one versioned English ambiguity marker, rounded up.
// A zero score does not establish clarity, completeness, correctness or PASS.
type Analysis struct {
	ProducerVersion    string       `json:"producer_version"`
	ContractHash       string       `json:"contract_hash"`
	Method             string       `json:"method"`
	AmbiguityScore     int          `json:"ambiguity_score"`
	StatementsExamined int          `json:"statements_examined"`
	FlaggedStatements  int          `json:"flagged_statements"`
	Suggestions        []Suggestion `json:"suggestions"`
}

func (SpecDoctor) Analyze(ctx context.Context, c intent.IntentContract) (Analysis, error) {
	if err := contextError(ctx); err != nil {
		return Analysis{}, err
	}
	f, err := c.Fields()
	if err != nil {
		return Analysis{}, err
	}
	assertions, _ := Assertions(c)
	hash, _ := c.ContentHash()
	r := Analysis{ProducerVersion: DoctorVersion, ContractHash: hash, Method: "english-marker-ratio.v1", Suggestions: []Suggestion{}}
	inspect := func(field, id, statement string) {
		r.StatementsExamined++
		if ambiguous(statement) {
			r.FlaggedStatements++
			r.Suggestions = append(r.Suggestions, Suggestion{Code: "DEFINE_OBSERVABLE_CRITERION", Field: field, AssertionID: id, Message: "Review vague or optional wording; specify observable behavior, conditions and a measurable acceptance criterion."})
		}
	}
	inspect("goal", "", f.Goal)
	for _, a := range assertions {
		if err := contextError(ctx); err != nil {
			return Analysis{}, err
		}
		if a.Field != "allowed_components" && a.Field != "forbidden_components" {
			inspect(a.Field, a.ID, a.Statement)
		}
	}
	if len(f.AcceptanceCriteria) == 0 {
		r.Suggestions = append(r.Suggestions, Suggestion{Code: "ADD_ACCEPTANCE_CRITERIA", Field: "acceptance_criteria", Message: "Add explicit observable acceptance criteria and bind each declaration to a reviewed verification check."})
	}
	r.AmbiguityScore = (100*r.FlaggedStatements + r.StatementsExamined - 1) / r.StatementsExamined
	return r, nil
}

func ambiguous(text string) bool {
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		switch word {
		case "fast", "quick", "easy", "simple", "secure", "robust", "appropriate", "reasonable", "etc", "maybe", "should", "might", "tbd", "todo":
			return true
		}
	}
	return false
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	return ctx.Err()
}
