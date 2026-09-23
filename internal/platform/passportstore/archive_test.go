package passportstore_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"safepatch/internal/domain"
	"safepatch/internal/domain/edition"
	"safepatch/internal/evidence"
	"safepatch/internal/platform/evidencestore"
	"safepatch/internal/platform/passportstore"
	"safepatch/internal/proof/passport"
	"safepatch/internal/trust"
)

func fixture(t *testing.T) (passport.Passport, evidence.ArchiveScope) {
	t.Helper()
	c := domain.Change{OrganizationID: "org", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	d := evidence.Digest([]byte("evidence"))
	p, err := passport.New(passport.Fields{Change: c, ChangeID: c.ID(), Commit: c.HeadSHA, RunID: "run", IntentDigest: d, ProofPlanDigest: d, EvidenceGraphDigest: d, ProofLevel: passport.UnknownLevel, PolicyDecision: passport.PolicyDecision{PolicyDigest: d, ResultDigest: d, Status: evidence.StatusUnknown}, VerificationDecision: trust.Unknown, IssuedAt: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return p, evidence.ArchiveScope{OrganizationID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: c.HeadSHA}}
}

func TestSignedLocalArchiveRoundTripAndTenantIsolation(t *testing.T) {
	p, scope := fixture(t)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))
	public := append(ed25519.PublicKey{}, key.Public().(ed25519.PublicKey)...)
	signer, err := passportstore.NewEd25519Signer("org", "key", key)
	if err != nil {
		t.Fatal(err)
	}
	key[0] ^= 1 // adapter must own a copy
	signed, err := signer.Sign(t.Context(), p)
	if err != nil || passport.Verify(signed, "org", "key", public) != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := evidencestore.NewLocal(dir, scope)
	if err != nil {
		t.Fatal(err)
	}
	archive := passportstore.Archive{Store: store}
	ref, err := archive.Put(t.Context(), signed)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := archive.Put(t.Context(), signed)
	if err != nil || ref != retry {
		t.Fatal("immutable retry", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = evidencestore.NewLocal(dir, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	archive.Store = store
	read, err := archive.Get(t.Context(), scope, ref)
	if err != nil || read.Digest() != signed.Digest() || passport.Verify(read, "org", "key", public) != nil {
		t.Fatal("reopen failed", err)
	}
	for _, mutate := range []func(*evidence.ArchiveScope){func(s *evidence.ArchiveScope) { s.OrganizationID = "other" }, func(s *evidence.ArchiveScope) { s.Subject.RepositoryID = "other" }, func(s *evidence.ArchiveScope) { s.Subject.Revision = strings.Repeat("c", 40) }} {
		other := scope
		mutate(&other)
		if _, err := archive.Get(t.Context(), other, ref); err == nil {
			t.Fatal("cross scope read")
		}
	}
	f, _ := p.Fields()
	f.Change.OrganizationID = "other"
	f.ChangeID = f.Change.ID()
	foreign, _ := passport.New(f)
	if _, err := signer.Sign(t.Context(), foreign); err == nil {
		t.Fatal("cross organization key use")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := archive.Put(ctx, p); err == nil {
		t.Fatal("cancelled storage")
	}
	if _, err := signer.Sign(ctx, p); err == nil {
		t.Fatal("cancelled signing")
	}
}

type poisonStore struct{ raw []byte }

func (s poisonStore) Put(context.Context, evidence.ArchiveScope, []byte) (evidence.OriginalReference, error) {
	return evidence.OriginalReference{SHA256: strings.Repeat("a", 64), Bytes: 1}, nil
}
func (s poisonStore) Get(context.Context, evidence.ArchiveScope, evidence.OriginalReference) ([]byte, error) {
	return s.raw, nil
}

func TestArchiveRejectsPoisonedStorage(t *testing.T) {
	p, scope := fixture(t)
	raw, _ := p.MarshalJSON()
	ref := evidence.OriginalReference{SHA256: evidence.Digest(raw), Bytes: int64(len(raw))}
	a := passportstore.Archive{Store: poisonStore{raw: bytes.Replace(raw, []byte(`"run_id":"run"`), []byte(`"run_id":"bad"`), 1)}}
	if _, err := a.Get(t.Context(), scope, ref); err == nil {
		t.Fatal("tampered bytes")
	}
	if _, err := a.Put(t.Context(), p); err == nil {
		t.Fatal("false persistence receipt")
	}
	a.Store = poisonStore{raw: raw}
	scope.OrganizationID = "other"
	if _, err := a.Get(t.Context(), scope, ref); err == nil {
		t.Fatal("scope lying store")
	}
}

func TestEveryEditionUsesOnePortableSchema(t *testing.T) {
	p, _ := fixture(t)
	raw, _ := p.MarshalJSON()
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			// Portable export and validation have no entitlement or edition parameter.
			decoded, err := passport.Decode(raw)
			if err != nil || decoded.Digest() != p.Digest() {
				t.Fatal(err)
			}
			var doc map[string]json.RawMessage
			if json.Unmarshal(raw, &doc) != nil || len(doc) != 3 || string(doc["schema"]) != `"safepatch.passport.v1"` {
				t.Fatal("schema drift")
			}
			var payload map[string]json.RawMessage
			if json.Unmarshal(doc["payload"], &payload) != nil || len(payload) != 14 {
				t.Fatal("missing shared fields", len(payload))
			}
			for _, k := range []string{"change_id", "intent_digest", "commit", "artifact_digest", "sbom", "provenance", "proof_level", "policy_decision", "evidence_graph_digest"} {
				if _, ok := payload[k]; !ok {
					t.Fatal("missing field", k)
				}
			}
		})
	}
}
