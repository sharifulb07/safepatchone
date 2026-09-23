// Package organization defines SaaS ownership records. Commercial configuration
// belongs to organization subscriptions/grants, never users or repositories.
package organization

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalidEntity = errors.New("invalid organization entity")
	ErrNotFound      = errors.New("organization entity not found")
	ErrConflict      = errors.New("organization entity conflict")
	ErrData          = errors.New("invalid organization storage data")
	idPattern        = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
	bundlePattern    = regexp.MustCompile(`^(community|developer|team|business|enterprise)-[1-9][0-9]{3}-v[1-9][0-9]{0,8}$`)
)

const MaxPageSize = 200

func ValidID(s string) bool { return len(s) <= 128 && idPattern.MatchString(s) }
func validName(s string) bool {
	if len(s) > 256 || strings.TrimSpace(s) == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}
func ValidTime(t time.Time) bool {
	return !t.IsZero() && t.UTC().Year() >= 1 && t.UTC().Year() <= 9999 && t.Nanosecond()%1000 == 0
}

type State string

const (
	Pending   State = "PENDING"
	Active    State = "ACTIVE"
	Suspended State = "SUSPENDED"
)

type User struct {
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
}

func (u User) Validate() error {
	if !ValidID(u.UserID) || !validName(u.DisplayName) || !ValidTime(u.CreatedAt) {
		return ErrInvalidEntity
	}
	return nil
}

type Organization struct {
	OrganizationID string    `json:"organization_id"`
	Name           string    `json:"name"`
	State          State     `json:"state"`
	CreatedAt      time.Time `json:"created_at"`
}

func (o Organization) Validate() error {
	if !ValidID(o.OrganizationID) || !validName(o.Name) || !ValidTime(o.CreatedAt) || (o.State != Pending && o.State != Active && o.State != Suspended) {
		return ErrInvalidEntity
	}
	return nil
}

type Project struct {
	OrganizationID string    `json:"organization_id"`
	ProjectID      string    `json:"project_id"`
	Name           string    `json:"name"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
}

func (p Project) Validate() error {
	if !ValidID(p.OrganizationID) || !ValidID(p.ProjectID) || p.ProjectID == p.OrganizationID || !validName(p.Name) || !ValidTime(p.CreatedAt) {
		return ErrInvalidEntity
	}
	return nil
}

type Repository struct {
	OrganizationID string    `json:"organization_id"`
	ProjectID      string    `json:"project_id"`
	RepositoryID   string    `json:"repository_id"`
	Name           string    `json:"name"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
}

func (r Repository) Validate() error {
	if !ValidID(r.OrganizationID) || !ValidID(r.ProjectID) || r.ProjectID == r.OrganizationID || !ValidID(r.RepositoryID) || r.RepositoryID == r.OrganizationID || !validName(r.Name) || !ValidTime(r.CreatedAt) {
		return ErrInvalidEntity
	}
	return nil
}

type SubscriptionState string

const (
	SubscriptionPending SubscriptionState = "PENDING"
	Trialing            SubscriptionState = "TRIALING"
	Subscribed          SubscriptionState = "ACTIVE"
	PastDue             SubscriptionState = "PAST_DUE"
	Canceled            SubscriptionState = "CANCELED"
	Expired             SubscriptionState = "EXPIRED"
)

func (s SubscriptionState) Valid() bool {
	switch s {
	case SubscriptionPending, Trialing, Subscribed, PastDue, Canceled, Expired:
		return true
	}
	return false
}

type Subscription struct {
	OrganizationID    string            `json:"organization_id"`
	SubscriptionID    string            `json:"subscription_id"`
	EditionVersionID  string            `json:"edition_version_id"`
	Provider          string            `json:"provider"`
	ExternalReference string            `json:"external_reference"`
	State             SubscriptionState `json:"state"`
	Revision          int64             `json:"revision"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

func (s Subscription) Validate() error {
	if !ValidID(s.OrganizationID) || !ValidID(s.SubscriptionID) || !bundlePattern.MatchString(s.EditionVersionID) || len(s.EditionVersionID) > 64 || !ValidID(s.Provider) || !ValidID(s.ExternalReference) || !s.State.Valid() || s.Revision < 1 || !ValidTime(s.CreatedAt) || !ValidTime(s.UpdatedAt) || s.UpdatedAt.Before(s.CreatedAt) {
		return ErrInvalidEntity
	}
	return nil
}

// Store is for trusted provisioning/application services. IDs are not authority.
// Writes require separate actor permission or authenticated billing provenance.
// Membership and entitlement records reuse their existing core contracts.
type Store interface {
	CreateUser(context.Context, User) error
	GetUser(context.Context, string) (User, error)
	CreateOrganization(context.Context, Organization) error
	GetOrganization(context.Context, string) (Organization, error)
	CreateProject(context.Context, string, Project) error
	GetProject(context.Context, string, string) (Project, error)
	CreateRepository(context.Context, string, Repository) error
	GetRepository(context.Context, string, string) (Repository, error)
	CreateSubscription(context.Context, string, Subscription) error
	GetSubscription(context.Context, string, string) (Subscription, error)
	UpdateSubscription(context.Context, string, Subscription, int64) error
}
