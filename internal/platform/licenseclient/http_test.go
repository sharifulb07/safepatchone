package licenseclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"safepatch/internal/licensing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(status int, raw string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(raw)), Header: make(http.Header)}
}
func TestRefreshTransportBindingAndFailureClassification(t *testing.T) {
	for _, mode := range []string{"active", "revoked", "denied", "unknown", "foreign", "old_nonce", "duplicate", "oversize", "redirect", "unauthorized", "outage", "rate_limit", "network", "certificate", "injected_field"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := New(Config{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() != Endpoint || req.Method != "POST" || req.URL.RawQuery != "" || req.Header.Get("Authorization") != "" {
					t.Fatal("unsafe destination or credentials")
				}
				var input map[string]string
				if json.NewDecoder(req.Body).Decode(&input) != nil || len(input) != 3 || input["organization_id"] != "org" || input["license_id"] != "license" || len(input["nonce"]) != 64 {
					t.Fatal("invalid request")
				}
				body := map[string]any{"organization_id": "org", "license_id": "license", "nonce": input["nonce"], "status": "active", "envelope": map[string]string{"fixture": "signature validation belongs to provider"}}
				switch mode {
				case "revoked", "denied", "unknown":
					body["status"] = mode
					body["envelope"] = nil
				case "foreign":
					body["organization_id"] = "other"
				case "old_nonce":
					body["nonce"] = strings.Repeat("0", 64)
				case "duplicate":
					return reply(200, `{"status":"active","status":"revoked"}`), nil
				case "oversize":
					return reply(200, strings.Repeat("x", licensing.MaxLicenseBytes+4097)), nil
				case "redirect":
					r := reply(302, "")
					r.Header.Set("Location", "http://169.254.169.254/latest/meta-data/")
					return r, nil
				case "unauthorized":
					return reply(401, "secret upstream text"), nil
				case "outage":
					return reply(503, "secret upstream text"), nil
				case "rate_limit":
					return reply(429, ""), nil
				case "network":
					return nil, &net.DNSError{IsTimeout: true, Err: "secret"}
				case "certificate":
					return nil, &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}
				case "injected_field":
					body["public_key"] = "attacker"
				}
				raw, _ := json.Marshal(body)
				return reply(200, string(raw)), nil
			})})
			got, err := c.RefreshLicense(t.Context(), "org", "license")
			if calls != 1 {
				t.Fatal("followed redirect", calls)
			}
			switch mode {
			case "active", "revoked", "denied":
				if err != nil || got.Status != mode {
					t.Fatal(got, err)
				}
			case "outage", "rate_limit", "network":
				if !errors.Is(err, licensing.ErrRefreshUnavailable) {
					t.Fatal(err)
				}
			default:
				if !errors.Is(err, licensing.ErrRefreshRejected) {
					t.Fatal(err)
				}
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("credential leak")
			}
		})
	}
}
func TestRefreshInputsAndCancellationDoNotReachTransport(t *testing.T) {
	c := New(Config{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached transport")
		return nil, nil
	})})
	for _, org := range []string{"", "https://attacker", "org/../other", "org\r\n"} {
		if _, err := c.RefreshLicense(t.Context(), org, "license"); err == nil {
			t.Fatal("invalid org")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.RefreshLicense(ctx, "org", "license"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestDefaultTransportHasNoProxyAndNoRedirects(t *testing.T) {
	c := New(Config{})
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil || tr.TLSClientConfig.InsecureSkipVerify || c.http.Timeout <= 0 {
		t.Fatal("unsafe transport defaults")
	}
	if c.http.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect allowed")
	}
}
