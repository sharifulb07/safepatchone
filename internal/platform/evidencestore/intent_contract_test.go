package evidencestore

import (
	"bytes"
	"testing"

	"safepatch/internal/domain/edition"
	"safepatch/internal/intent"
)

func TestIntentContractUsesExistingImmutableScopedStorage(t *testing.T) {
	s := scope()
	store, err := NewLocal(t.TempDir(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	contract, err := intent.NewIntentContract(intent.IntentContractFields{Goal: "Preserve receipts", Requirements: []string{"Retry safely"}, DatabaseConstraints: []string{"Retain historical records"}, AllowedComponents: []string{"payments"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := contract.CanonicalJSON()
	ref, err := store.Put(t.Context(), s, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, offering := range edition.Editions() {
		t.Run(offering.Name, func(t *testing.T) {
			stored, err := store.Get(t.Context(), s, ref)
			if err != nil || !bytes.Equal(stored, raw) {
				t.Fatal("stored contract changed", err)
			}
			c, err := intent.DecodeIntentContract(stored)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := c.ContentHash()
			want, _ := contract.ContentHash()
			if got != want {
				t.Fatal("hash changed")
			}
		})
	}
	for _, kind := range []string{"tenant", "repository", "revision"} {
		foreign := s
		switch kind {
		case "tenant":
			foreign.OrganizationID = "other"
		case "repository":
			foreign.Subject.RepositoryID = "other"
		case "revision":
			foreign.Subject.Revision = "other"
		}
		if _, err := store.Get(t.Context(), foreign, ref); err == nil {
			t.Fatal("cross-scope read", kind)
		}
		if _, err := store.Put(t.Context(), foreign, raw); err == nil {
			t.Fatal("cross-scope write", kind)
		}
	}
	f, _ := contract.Fields()
	f.Goal = "Add refund support"
	updated, err := intent.NewIntentContract(f)
	if err != nil {
		t.Fatal(err)
	}
	newRaw, _ := updated.CanonicalJSON()
	newRef, err := store.Put(t.Context(), s, newRaw)
	if err != nil || newRef == ref {
		t.Fatal("revision replaced previous contract", err)
	}
	previous, err := store.Get(t.Context(), s, ref)
	if err != nil || !bytes.Equal(previous, raw) {
		t.Fatal("old contract lost", err)
	}
}
