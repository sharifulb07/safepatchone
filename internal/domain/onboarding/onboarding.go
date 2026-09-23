// Package onboarding records setup intent. Nothing in this package grants access.
package onboarding

import (
	"context"
	"errors"
	"regexp"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/organization"
	"strings"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid onboarding request")
	ErrConflict    = errors.New("onboarding changed; reload and retry")
	ErrNotReady    = errors.New("onboarding prerequisite not ready")
	ErrUnavailable = errors.New("onboarding dependency unavailable")
)

type Repository struct {
	ID       string `json:"id"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)

func (r Repository) Validate() error {
	if !organization.ValidID(r.ID) || !repoName.MatchString(r.FullName) {
		return ErrInvalid
	}
	for _, part := range strings.Split(r.FullName, "/") {
		if part == "." || part == ".." {
			return ErrInvalid
		}
	}
	return nil
}

type State struct {
	OrganizationID   string       `json:"organization_id"`
	Revision         int64        `json:"revision"`
	Edition          edition.Key  `json:"edition"`
	EditionVersionID string       `json:"edition_version_id"`
	InstallationID   int64        `json:"installation_id"`
	Repositories     []Repository `json:"repositories"`
	PolicyID         string       `json:"policy_id"`
	ProofID          string       `json:"proof_id"`
}

func (s State) Validate() error {
	if !organization.ValidID(s.OrganizationID) || s.Revision < 1 || s.InstallationID < 0 || len(s.Repositories) > 50 {
		return ErrInvalid
	}
	if s.Edition != "" {
		if _, err := edition.Lookup(s.Edition); err != nil || !organization.ValidID(s.EditionVersionID) {
			return ErrInvalid
		}
	} else if s.EditionVersionID != "" {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range s.Repositories {
		if r.Validate() != nil || seen[r.ID] || s.InstallationID == 0 {
			return ErrInvalid
		}
		seen[r.ID] = true
	}
	if s.PolicyID != "" && (!organization.ValidID(s.PolicyID) || len(s.Repositories) == 0) {
		return ErrInvalid
	}
	if s.ProofID != "" && (!organization.ValidID(s.ProofID) || s.PolicyID == "") {
		return ErrInvalid
	}
	return nil
}

type Access struct {
	Ready     bool       `json:"ready"`
	Source    string     `json:"source"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// Access must read primary, authoritative subscription/trial/license state.
// It must never resolve from State.Edition, checkout URLs or request metadata.
type Store interface {
	Create(context.Context, string, string, string) (State, error) // actor, immutable attempt ID, organization name
	Load(context.Context, string) (State, error)
	Save(context.Context, State, int64) (State, error)
	Access(context.Context, string, string) (Access, error)
}
