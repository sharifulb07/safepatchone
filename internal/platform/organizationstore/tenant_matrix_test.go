package organizationstore

import (
	"database/sql/driver"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/tenant"
	"testing"
)

func TestPostgresTenantIsolationAcrossEveryEditionPair(t *testing.T) {
	for _, a := range edition.Editions() {
		for _, b := range edition.Editions() {
			t.Run(a.Name+"_to_"+b.Name, func(t *testing.T) {
				aOrg, bOrg := "a_"+string(a.Key), "b_"+string(b.Key)
				// Same repository identifier deliberately exists in other organizations.
				// All capabilities are granted so commercial denial cannot hide a leak.
				store := scripted(t, memberStep(aOrg, authorization.RoleOwner, true), step{sql: "WHERE r.organization_id=$1 AND r.repository_id=$2", args: []driver.Value{aOrg, "same_repo"}, rows: [][]driver.Value{{"same_repo"}}}, memberStep(aOrg, authorization.RoleOwner, true), step{sql: "WHERE m.organization_id=$1 AND m.user_id=$2", args: []driver.Value{bOrg, "user"}})
				permissions, _ := authorization.NewPermissionService(store)
				guard, _ := application.NewExecutionGuard(store, grants(true), permissions, unmetered{}, application.Requirement{Feature: feature.KeyVerifyBasic, Permission: authorization.KeyProofExecute})
				actor := authorization.Principal{ID: "user", Kind: authorization.User}
				if _, err := guard.Authorize(t.Context(), actor, tenant.Scope{OrganizationID: aOrg, ResourceID: "same_repo"}); err != nil {
					t.Fatal(err)
				}
				if _, err := guard.Authorize(t.Context(), actor, tenant.Scope{OrganizationID: bOrg, ResourceID: "same_repo"}); err == nil {
					t.Fatal("edition bypassed membership")
				}
			})
		}
	}
}
