package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"safepatch/internal/domain/organization"
)

var commitSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)
var ErrInvalidChange = errors.New("invalid immutable change identity")

// Change pins both sides of a change. Names, PR numbers and delivery IDs cannot
// substitute for source identity. The identity algorithm is shared by local/CI.
type Change struct {
	OrganizationID string `json:"organization_id"`
	RepositoryID   string `json:"repository_id"`
	BaseSHA        string `json:"base_sha"`
	HeadSHA        string `json:"head_sha"`
}

func ValidCommitSHA(s string) bool { return commitSHA.MatchString(s) }
func (c Change) Validate() error {
	if !organization.ValidID(c.OrganizationID) || !organization.ValidID(c.RepositoryID) || !ValidCommitSHA(c.BaseSHA) || !ValidCommitSHA(c.HeadSHA) {
		return ErrInvalidChange
	}
	return nil
}
func (c Change) ID() string {
	if c.Validate() != nil {
		return ""
	}
	hash := sha256.Sum256([]byte("safepatch.change.v1\n" + c.OrganizationID + "\n" + c.RepositoryID + "\n" + c.BaseSHA + "\n" + c.HeadSHA))
	return "change_" + hex.EncodeToString(hash[:])
}
