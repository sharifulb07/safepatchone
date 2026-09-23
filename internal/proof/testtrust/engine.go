package testtrust

import (
	"context"
	"math/big"

	"safepatch/internal/evidence"
)

type Decision string

const NoRegression Decision = "NO_REGRESSION"
const Regression Decision = "REGRESSION"
const Unknown Decision = "UNKNOWN"

type Finding struct {
	Code        string                  `json:"code"`
	TestID      string                  `json:"test_id"`
	AssertionID string                  `json:"assertion_id"`
	Source      evidence.SourceLocation `json:"source"`
}

// Result is diagnostic test evidence, never a PASS or production approval.
// MutationStatus remains NOT_RUN unless a separate admitted executor is added.
type Result struct {
	Schema         string    `json:"schema"`
	OrganizationID string    `json:"organization_id"`
	RepositoryID   string    `json:"repository_id"`
	BaseRevision   string    `json:"base_revision"`
	HeadRevision   string    `json:"head_revision"`
	BaseDigest     string    `json:"base_digest"`
	HeadDigest     string    `json:"head_digest"`
	Decision       Decision  `json:"decision"`
	Findings       []Finding `json:"findings"`
	Incomplete     bool      `json:"incomplete"`
	MutationStatus string    `json:"mutation_status"`
}

func Unavailable() Result {
	return Result{Schema: "safepatch.testtrust.v1", Decision: Unknown, Findings: []Finding{}, Incomplete: true, MutationStatus: "NOT_RUN"}
}

type Engine struct{}

func (Engine) Compare(ctx context.Context, base, head Snapshot) (Result, error) {
	r := Unavailable()
	if ctx == nil {
		return r, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	a, e1 := base.Fields()
	b, e2 := head.Fields()
	if e1 != nil || e2 != nil || a.OrganizationID != b.OrganizationID || a.Subject.RepositoryID != b.Subject.RepositoryID || a.Producer != b.Producer || a.ProducerVersion != b.ProducerVersion {
		return r, ErrInvalid
	}
	if a.Subject.Revision == b.Subject.Revision && base.Digest() != head.Digest() {
		return r, ErrInvalid
	}
	r.OrganizationID = a.OrganizationID
	r.RepositoryID = a.Subject.RepositoryID
	r.BaseRevision = a.Subject.Revision
	r.HeadRevision = b.Subject.Revision
	r.BaseDigest = base.Digest()
	r.HeadDigest = head.Digest()
	r.Incomplete = !a.Complete || !b.Complete || len(a.Tests) == 0 || len(b.Tests) == 0
	add := func(code string, t Test, id string) {
		r.Findings = append(r.Findings, Finding{Code: code, TestID: t.ID, AssertionID: id, Source: t.Source})
	}
	index := map[string]Test{}
	for _, t := range b.Tests {
		index[t.ID] = t
	}
	for _, t := range append(append([]Test{}, a.Tests...), b.Tests...) {
		if !t.Complete || t.Skip == "UNKNOWN" || t.Skip == "CONDITIONAL" || t.Negative == "UNKNOWN" {
			r.Incomplete = true
		}
		for _, assertion := range t.Assertions {
			if assertion.Relation == "UNKNOWN" {
				r.Incomplete = true
			}
		}
	}
	for _, old := range a.Tests {
		if err := ctx.Err(); err != nil {
			return Unavailable(), err
		}
		next, exists := index[old.ID]
		if !exists {
			if b.Complete {
				add("REMOVED_TEST", old, "")
				if old.Negative == "NEGATIVE" {
					add("NEGATIVE_TEST_REMOVED", old, "")
				}
			} else {
				r.Incomplete = true
			}
			continue
		}
		if old.Negative == "NEGATIVE" && next.Negative == "POSITIVE" {
			add("NEGATIVE_TEST_REMOVED", next, "")
		}
		assertions := map[string]Assertion{}
		for _, v := range next.Assertions {
			assertions[v.ID] = v
		}
		for _, v := range old.Assertions {
			n, ok := assertions[v.ID]
			if !ok {
				if next.Complete {
					add("REMOVED_ASSERTION", old, v.ID)
				} else {
					r.Incomplete = true
				}
				continue
			}
			if v.SubjectDigest != n.SubjectDigest {
				r.Incomplete = true
				add("ASSERTION_CHANGE_UNCLASSIFIED", next, n.ID)
				continue
			}
			if v.Relation == "UNKNOWN" || n.Relation == "UNKNOWN" {
				r.Incomplete = true
				if v.PredicateDigest != n.PredicateDigest {
					add("ASSERTION_CHANGE_UNCLASSIFIED", next, n.ID)
				}
				continue
			}
			if weaker(v, n) {
				add("WEAKENED_ASSERTION", next, n.ID)
			} else if !implies(n, v) {
				r.Incomplete = true
				add("ASSERTION_CHANGE_UNCLASSIFIED", next, n.ID)
			}
		}
	}
	// All skipped head tests are visible, including new and pre-existing skips.
	for _, t := range b.Tests {
		if t.Skip == "SKIPPED" || t.Skip == "CONDITIONAL" {
			add("SKIPPED_TEST", t, "")
		}
	}
	if !a.Coverage.Known || !b.Coverage.Known || a.Coverage.BasisDigest != b.Coverage.BasisDigest {
		r.Incomplete = true
	} else if b.Coverage.Covered*a.Coverage.Total < a.Coverage.Covered*b.Coverage.Total {
		r.Findings = append(r.Findings, Finding{Code: "COVERAGE_REDUCTION"})
	}
	r.Decision = NoRegression
	if r.Incomplete {
		r.Decision = Unknown
	}
	for _, f := range r.Findings {
		if f.Code != "ASSERTION_CHANGE_UNCLASSIFIED" {
			r.Decision = Regression
			break
		}
	}
	return r, nil
}
func holds(a Assertion, x *big.Rat) bool {
	bound, _ := number(a.Bound)
	c := x.Cmp(bound)
	switch a.Relation {
	case "EQ":
		return c == 0
	case "NE":
		return c != 0
	case "GT":
		return c > 0
	case "GE":
		return c >= 0
	case "LT":
		return c < 0
	case "LE":
		return c <= 0
	}
	return false
}

// Numeric predicates change truth only at their two boundaries. Testing the
// boundaries, midpoint and exterior points establishes implication exactly.
func implies(a, b Assertion) bool {
	x, _ := number(a.Bound)
	y, _ := number(b.Bound)
	lo, hi := x, y
	if lo.Cmp(hi) > 0 {
		lo, hi = hi, lo
	}
	points := []*big.Rat{lo, hi, new(big.Rat).Sub(lo, big.NewRat(1, 1)), new(big.Rat).Add(hi, big.NewRat(1, 1)), new(big.Rat).Quo(new(big.Rat).Add(lo, hi), big.NewRat(2, 1))}
	for _, p := range points {
		if holds(a, p) && !holds(b, p) {
			return false
		}
	}
	return true
}
func weaker(old, next Assertion) bool { return implies(old, next) && !implies(next, old) }
