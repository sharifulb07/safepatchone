package entitlement_test

import (
	"context"
	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"sync"
	"testing"
)

type multiTenantSnapshots struct {
	inputs map[string]entitlement.SnapshotInput
	poison string
}

func (s *multiTenantSnapshots) CurrentState(_ context.Context, org string) (domain.SnapshotState, error) {
	if s.poison != "" {
		return s.inputs[s.poison].State, nil
	}
	return s.inputs[org].State, nil
}
func (s *multiTenantSnapshots) Load(_ context.Context, org string) (entitlement.SnapshotInput, error) {
	return s.inputs[org], nil
}
func TestCacheCannotReuseAnotherTenantsGrantAcrossEditions(t *testing.T) {
	for _, a := range edition.Editions() {
		for _, b := range edition.Editions() {
			t.Run(a.Name+"_to_"+b.Name, func(t *testing.T) {
				aOrg, bOrg := "a_"+string(a.Key), "b_"+string(b.Key)
				source := &multiTenantSnapshots{inputs: map[string]entitlement.SnapshotInput{}}
				for _, item := range []struct {
					org     string
					ed      edition.Key
					enabled bool
				}{{aOrg, a.Key, false}, {bOrg, b.Key, true}} {
					state := snapshotState(item.org)
					state.Edition = item.ed
					state.EditionVersion = string(item.ed) + "-2026-v1"
					g := grant("same_id", domain.SourceEnterpriseLicense, item.enabled, 10)
					g.OrganizationID = item.org
					source.inputs[item.org] = entitlement.SnapshotInput{State: state, Entitlements: []domain.Entitlement{g}}
				}
				s := snapshotService(t, source, at, 2)
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for _, org := range []string{aOrg, bOrg} {
							got, err := s.Has(t.Context(), org, feature.KeyVerifyBasic)
							if err != nil || got != (org == bOrg) {
								t.Error("tenant cache collision", org, got, err)
							}
						}
					}()
				}
				wg.Wait()
				copy, err := s.Snapshot(t.Context(), bOrg)
				if err != nil {
					t.Fatal(err)
				}
				copy.Features[feature.KeyVerifyBasic] = false
				if got, err := s.Has(t.Context(), bOrg, feature.KeyVerifyBasic); err != nil || !got {
					t.Fatal("mutable cache return")
				}
				source.poison = bOrg
				if got, err := s.Has(t.Context(), aOrg, feature.KeyVerifyBasic); err == nil || got {
					t.Fatal("foreign state accepted on warm cache")
				}
			})
		}
	}
}
