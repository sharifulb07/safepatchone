package tenant

import (
	"errors"
	"regexp"
)

var ErrInvalidScope = errors.New("invalid tenant scope")
var opaqueID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// ValidID validates bounded opaque identities, never paths or query fragments.
func ValidID(id string) bool { return len(id) <= 128 && opaqueID.MatchString(id) }

// Scope binds a resource to an organization. A trusted tenant resolver must
// establish resource ownership in storage; syntax alone does not prove it.
// Organization-level actions use the organization ID as ResourceID.
type Scope struct {
	OrganizationID string
	ResourceID     string
}

func (s Scope) Validate() error {
	if !ValidID(s.OrganizationID) || !ValidID(s.ResourceID) {
		return ErrInvalidScope
	}
	return nil
}
