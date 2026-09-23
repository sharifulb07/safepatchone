package downloads_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/platform/downloads"
)

func TestPrivateArtifactBindingExpiryAndImmutableClassification(t *testing.T) {
	a, s, _, _ := fixture(t, "org")
	access := application.PlatformAccess{Tenants: tenants{a}, Entitlements: grants{a}, Permissions: a, Quotas: a}
	items := artifacts()
	items[2].OrganizationID = "victim"
	service, err := application.NewDownloads(access, items, s, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{ID: "owner", Kind: authorization.User}
	if _, err = service.Protected(t.Context(), actor, "org", "host"); err == nil || s.calls != 0 {
		t.Fatal("foreign artifact signed after valid tenant and entitlement")
	}
	items[2].OrganizationID = "" // source slice mutation must not change loaded bindings
	if _, err = service.Protected(t.Context(), actor, "org", "host"); err == nil {
		t.Fatal("mutable catalog")
	}
	items[2].AvailableUntil = time.Now().Add(-time.Second)
	service, _ = application.NewDownloads(access, items, s, time.Now)
	if _, err = service.Protected(t.Context(), actor, "org", "host"); err == nil || s.calls != 0 {
		t.Fatal("expired artifact signed")
	}
	items = artifacts()
	items[2].SHA256 = items[0].SHA256
	if _, err = application.NewDownloads(access, items, s, time.Now); err == nil {
		t.Fatal("private digest publicly aliased")
	}
	items = artifacts()
	items[2].ID = items[0].ID
	if _, err = application.NewDownloads(access, items, s, time.Now); err == nil {
		t.Fatal("duplicate artifact ID")
	}
}

func TestS3SigningDeadlineCredentialExpiryAndFixedOrigin(t *testing.T) {
	for _, mode := range []string{"normal", "expiring", "expired", "error", "empty"} {
		t.Run(mode, func(t *testing.T) {
			end := time.Now().Add(12 * time.Second)
			config := aws.Config{Region: "us-east-1", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
				c := aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "test-only-secret"}
				switch mode {
				case "expiring":
					c.CanExpire = true
					c.Expires = end
				case "expired":
					c.CanExpire = true
					c.Expires = time.Now().Add(-time.Second)
				case "error":
					return c, errors.New("private secret")
				case "empty":
					return aws.Credentials{}, nil
				}
				return c, nil
			})}
			s, err := downloads.NewS3Signer(config, "https://objects.example.test", "private-releases")
			if err != nil {
				t.Fatal(err)
			}
			artifact := artifacts()[2]
			artifact.ObjectVersion = "immutable+version/1"
			link, err := s.Sign(t.Context(), artifact, time.Now().Add(time.Minute))
			if mode == "expired" || mode == "error" || mode == "empty" {
				if err == nil || strings.Contains(err.Error(), "secret") {
					t.Fatal(mode, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			verifySignature(t, link.URL)
			u, _ := url.Parse(link.URL)
			if u.Query().Get("versionId") != artifact.ObjectVersion || u.Host != "objects.example.test" || u.Path != "/private-releases/"+artifact.Key() {
				t.Fatal("object identity changed")
			}
			if mode == "expiring" && link.ExpiresAt.After(end) {
				t.Fatal("outlived credential")
			}
			for _, delta := range []time.Duration{-time.Second, 0, 2 * time.Minute} {
				if _, err = s.Sign(t.Context(), artifact, time.Now().Add(delta)); err == nil {
					t.Fatal("invalid TTL")
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err = s.Sign(ctx, artifact, time.Now().Add(time.Minute)); err == nil {
				t.Fatal("cancelled signature")
			}
			for _, endpoint := range []string{"http://objects.example.test", "https://user:secret@host", "https://host/path", "https://host?key=other", "https://host/#fragment"} {
				if _, err = downloads.NewS3Signer(config, endpoint, "private-releases"); err == nil {
					t.Fatal(endpoint)
				}
			}
		})
	}
}

func TestDownloadConfigStrictAndBounded(t *testing.T) {
	c := downloads.Configuration{Schema: "safepatch.downloads.v1", Region: "us-east-1", Endpoint: "https://objects.example.test", Bucket: "private-releases", Artifacts: artifacts()}
	raw, _ := json.Marshal(c)
	if _, err := downloads.DecodeConfiguration(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"schema":`, `"Schema":`, 1), strings.Replace(string(raw), `"kind":`, `"Kind":`, 1), strings.Replace(string(raw), `"kind":"cli"`, `"kind":"cli","kind":"public_asset"`, 1), strings.Replace(string(raw), `"filename":"cli.zip"`, `"filename":"../secret"`, 1), string(raw) + `{}`, strings.Repeat(" ", 2*1024*1024) + string(raw)} {
		if _, err := downloads.DecodeConfiguration([]byte(bad)); err == nil {
			t.Fatal("unsafe config accepted")
		}
	}
}
