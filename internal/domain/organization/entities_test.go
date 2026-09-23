package organization_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	org "safepatch/internal/domain/organization"
)

func TestSharedEntitiesAndSubscriptionOwnership(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	entities := []interface{ Validate() error }{org.User{UserID: "user", DisplayName: "User", Active: true, CreatedAt: now}, org.Organization{OrganizationID: "org", Name: "Organization", State: org.Active, CreatedAt: now}, org.Project{OrganizationID: "org", ProjectID: "project", Name: "Project", Active: true, CreatedAt: now}, org.Repository{OrganizationID: "org", ProjectID: "project", RepositoryID: "repo", Name: "Repo", Active: true, CreatedAt: now}}
	for _, e := range entities {
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, edition := range []string{"community", "developer", "team", "business", "enterprise"} {
		s := org.Subscription{OrganizationID: "org", SubscriptionID: "sub", EditionVersionID: edition + "-2026-v1", Provider: "manual", ExternalReference: "contract", State: org.Subscribed, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(s)
		for _, forbidden := range []string{"repository_id", "user_id", "product_version"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatal("subscription has wrong owner/version")
			}
		}
	}
}
func TestEntitiesRejectMalformedScopeAndUnsafeMetadata(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"", "../org", "org;DROP", strings.Repeat("a", 129)} {
		if org.ValidID(id) {
			t.Fatal(id)
		}
		if (org.Repository{OrganizationID: id, ProjectID: "project", RepositoryID: "repo", Name: "Repo", CreatedAt: now}).Validate() == nil {
			t.Fatal("invalid owner")
		}
	}
	for _, name := range []string{"", "  ", "test\n", "x\u202E", strings.Repeat("a", 257)} {
		if (org.User{UserID: "user", DisplayName: name, CreatedAt: now}).Validate() == nil {
			t.Fatal("unsafe name")
		}
	}
	for _, stamp := range []time.Time{{}, now.Add(time.Nanosecond), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if org.ValidTime(stamp) {
			t.Fatal("unsafe timestamp")
		}
	}
	if (org.Organization{OrganizationID: "org", Name: "Org", State: "UNKNOWN", CreatedAt: now}).Validate() == nil {
		t.Fatal("unknown organization state")
	}
	if (org.Project{OrganizationID: "org", ProjectID: "org", Name: "P", CreatedAt: now}).Validate() == nil {
		t.Fatal("ambiguous scope")
	}
	for _, field := range []string{"project", "repository"} {
		r := org.Repository{OrganizationID: "org", ProjectID: "project", RepositoryID: "repo", Name: "Repo", CreatedAt: now}
		if field == "project" {
			r.ProjectID = r.OrganizationID
		} else {
			r.RepositoryID = r.OrganizationID
		}
		if r.Validate() == nil {
			t.Fatalf("repository accepted organization scope as %s identity", field)
		}
	}
	s := org.Subscription{OrganizationID: "org", SubscriptionID: "sub", EditionVersionID: "team-2026-v1", Provider: "manual", ExternalReference: "contract", State: org.Subscribed, Revision: 1, CreatedAt: now, UpdatedAt: now}
	for _, mode := range []string{"bundle", "state", "revision", "time", "reference"} {
		bad := s
		switch mode {
		case "bundle":
			bad.EditionVersionID = "1.5.2"
		case "state":
			bad.State = "PAID_FOREVER"
		case "revision":
			bad.Revision = 0
		case "time":
			bad.UpdatedAt = now.Add(-time.Second)
		case "reference":
			bad.ExternalReference = "https://secret"
		}
		if bad.Validate() == nil {
			t.Fatal(mode)
		}
	}
}
