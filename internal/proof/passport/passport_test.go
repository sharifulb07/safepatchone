package passport_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"safepatch/internal/domain"
	"safepatch/internal/evidence"
	"safepatch/internal/proof"
	"safepatch/internal/proof/passport"
	"safepatch/internal/trust"
)

func fields() passport.Fields {
	c := domain.Change{OrganizationID: "org_a", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	digest := evidence.Digest([]byte("immutable bytes"))
	return passport.Fields{Change: c, ChangeID: c.ID(), RunID: "run", IntentDigest: digest, Commit: c.HeadSHA,
		ArtifactDigest: &digest, SBOM: &passport.Reference{Digest: digest, MediaType: "application/spdx+json"}, Provenance: &passport.Reference{Digest: digest, MediaType: "application/json"},
		ProofLevel: proof.L2, ProofPlanDigest: digest, PolicyDecision: passport.PolicyDecision{PolicyDigest: digest, ResultDigest: digest, Status: evidence.StatusPass}, EvidenceGraphDigest: digest, VerificationDecision: trust.Pass, IssuedAt: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)}
}

func TestRoundTripImmutableAndExplicitUnknown(t *testing.T) {
	f := fields()
	p, err := passport.New(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := p.MarshalJSON()
	f.SBOM.Digest = "changed"
	*f.ArtifactDigest = "changed"
	copy, _ := p.Fields()
	copy.Provenance.Digest = "changed"
	if now, _ := p.MarshalJSON(); !bytes.Equal(raw, now) {
		t.Fatal("mutable Passport")
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	decoded, err := passport.Decode(pretty.Bytes())
	if err != nil || decoded.Digest() != p.Digest() {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &p) == nil || p.Digest() != decoded.Digest() {
		t.Fatal("overwrote sealed value")
	}
	f = fields()
	f.ArtifactDigest = nil
	f.SBOM = nil
	f.Provenance = nil
	f.ProofLevel = passport.UnknownLevel
	f.VerificationDecision = trust.Unknown
	f.PolicyDecision.Status = evidence.StatusUnknown
	p, err = passport.New(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = p.MarshalJSON()
	for _, key := range []string{`"artifact_digest":null`, `"sbom":null`, `"provenance":null`, `"proof_level":"UNKNOWN"`, `"signature":null`} {
		if !bytes.Contains(raw, []byte(key)) {
			t.Fatalf("missing %s", key)
		}
	}
}

func TestSignaturesBindEveryFieldAndExternalTrust(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	public := key.Public().(ed25519.PublicKey)
	p, _ := passport.New(fields())
	signed, err := passport.Sign(p, "key_1", key)
	if err != nil || passport.Verify(signed, "org_a", "key_1", public) != nil {
		t.Fatal(err)
	}
	retry, _ := passport.Sign(p, "key_1", key)
	if retry.Digest() != signed.Digest() {
		t.Fatal("signing retry changed bytes")
	}
	raw, _ := signed.MarshalJSON()
	decoded, err := passport.Decode(raw)
	if err != nil || passport.Verify(decoded, "org_a", "key_1", public) != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		org, id string
		key     ed25519.PublicKey
	}{{"org_b", "key_1", public}, {"org_a", "revoked", public}, {"org_a", "key_1", ed25519.PublicKey(bytes.Repeat([]byte{9}, 32))}, {"org_a", "key_1", nil}} {
		if passport.Verify(signed, tc.org, tc.id, tc.key) == nil {
			t.Fatal("untrusted signature accepted")
		}
	}
	if passport.Verify(p, "org_a", "key_1", public) == nil {
		t.Fatal("unsigned accepted")
	}
	if _, err := passport.Sign(signed, "key_1", key); err == nil {
		t.Fatal("silently replaced signature")
	}
	for _, pair := range [][2]string{{`"run_id":"run"`, `"run_id":"other"`}, {`"proof_level":"L2"`, `"proof_level":"L8"`}, {`"application/spdx+json"`, `"application/json"`}, {`"2026-09-07T12:00:00Z"`, `"2026-09-07T13:00:00Z"`}, {`"key_id":"key_1"`, `"key_id":"key_2"`}} {
		altered, err := passport.Decode(bytes.Replace(raw, []byte(pair[0]), []byte(pair[1]), 1))
		if err != nil {
			t.Fatal("test tamper must remain structurally valid", err)
		}
		if passport.Verify(altered, "org_a", "key_1", public) == nil {
			t.Fatal("tamper accepted")
		}
	}
	sig, _ := signed.Signature()
	sig.Value[0] ^= 1
	if passport.Verify(signed, "org_a", "key_1", public) != nil {
		t.Fatal("signature was mutable")
	}
	key[63] ^= 1
	if _, err := passport.Sign(p, "key_1", key); err == nil {
		t.Fatal("invalid private key accepted")
	}
}

func TestRejectAmbiguousHostileAndInconsistentPassports(t *testing.T) {
	p, _ := passport.New(fields())
	raw, _ := p.MarshalJSON()
	for _, input := range [][]byte{
		nil, []byte(`null`), append(raw, raw...), bytes.Repeat([]byte{' '}, passport.MaxBytes+1),
		bytes.Replace(raw, []byte(`"run_id":"run"`), []byte(`"run_id":"run","run_id":"other"`), 1),
		bytes.Replace(raw, []byte(`"run_id"`), []byte(`"RUN_ID"`), 1),
		bytes.Replace(raw, []byte(`"run_id":"run"`), []byte(`"run_id":null`), 1),
		bytes.Replace(raw, []byte(`"run_id":"run",`), nil, 1),
		bytes.Replace(raw, []byte(`"signature":null`), []byte(`"signature":null,"edition":"enterprise"`), 1),
		bytes.Replace(raw, []byte(`"key_id"`), []byte(`"KEY_ID"`), 1), // unchanged unsigned document excluded below
		bytes.Replace(raw, []byte(`"media_type":"application/spdx+json"`), []byte(`"media_type":"application/spdx+json","url":"http://169.254.169.254"`), 1),
	} {
		if bytes.Equal(input, raw) {
			continue
		}
		if _, err := passport.Decode(input); err == nil {
			t.Fatal("hostile encoding accepted", string(input))
		}
	}
	for name, mutate := range map[string]func(*passport.Fields){
		"change":              func(f *passport.Fields) { f.ChangeID = "change_forged" },
		"commit":              func(f *passport.Fields) { f.Commit = strings.Repeat("c", 40) },
		"cross_tenant":        func(f *passport.Fields) { f.Change.OrganizationID = "org_b" },
		"digest":              func(f *passport.Fields) { f.IntentDigest = strings.Repeat("A", 64) },
		"unknown_pass":        func(f *passport.Fields) { f.ProofLevel = passport.UnknownLevel },
		"policy_unknown_pass": func(f *passport.Fields) { f.PolicyDecision.Status = evidence.StatusUnknown },
		"future_level":        func(f *passport.Fields) { f.ProofLevel = "L9" },
		"path":                func(f *passport.Fields) { f.SBOM.Digest = "../../secret" },
		"time":                func(f *passport.Fields) { f.IssuedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			f := fields()
			mutate(&f)
			if _, err := passport.New(f); err == nil {
				t.Fatal("invalid fields accepted")
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	p, _ := passport.New(fields())
	raw, _ := p.MarshalJSON()
	f.Add(raw)
	f.Add([]byte(`{"schema":null}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := passport.Decode(raw)
		if err == nil {
			encoded, err := p.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			again, err := passport.Decode(encoded)
			if err != nil || again.Digest() != p.Digest() {
				t.Fatal("unstable decoding", err)
			}
		}
	})
}
