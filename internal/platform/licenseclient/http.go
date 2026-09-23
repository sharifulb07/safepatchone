// Package licenseclient contains the optional connected deployment transport.
// Air-gapped composition must not construct or run this adapter.
package licenseclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/licensing"
)

const Endpoint = "https://safepatch.dev/api/v1/licenses/status"

type Config struct {
	// Transport is trusted process composition, never repository/license input.
	Transport http.RoundTripper
}
type Client struct{ http *http.Client }

func New(c Config) *Client {
	t := c.Transport
	if t == nil {
		t = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxConnsPerHost: 2, IdleConnTimeout: time.Minute, ForceAttemptHTTP2: true}
	}
	return &Client{&http.Client{Transport: t, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) RefreshLicense(ctx context.Context, org, licenseID string) (licensing.RefreshResult, error) {
	failure := licensing.RefreshResult{}
	if c == nil || c.http == nil || ctx == nil || !entitlement.ValidID(org) || !entitlement.ValidID(licenseID) {
		return failure, licensing.ErrRefreshRejected
	}
	if err := ctx.Err(); err != nil {
		return failure, err
	}
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return failure, licensing.ErrRefreshRejected
	}
	nonce := hex.EncodeToString(nonceBytes)
	raw, _ := json.Marshal(map[string]string{"organization_id": org, "license_id": licenseID, "nonce": nonce})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(raw))
	if err != nil {
		return failure, licensing.ErrRefreshRejected
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-store")
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return failure, ctx.Err()
		}
		var cert *tls.CertificateVerificationError
		var unknown x509.UnknownAuthorityError
		if errors.As(err, &cert) || errors.As(err, &unknown) {
			return failure, licensing.ErrRefreshRejected
		}
		var network net.Error
		if errors.As(err, &network) {
			return failure, licensing.ErrRefreshUnavailable
		}
		return failure, licensing.ErrRefreshRejected
	}
	defer res.Body.Close()
	if res.StatusCode == 408 || res.StatusCode == 429 || res.StatusCode >= 500 && res.StatusCode <= 599 {
		return failure, licensing.ErrRefreshUnavailable
	}
	if res.StatusCode != http.StatusOK {
		return failure, licensing.ErrRefreshRejected
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, licensing.MaxLicenseBytes+4097))
	if err != nil {
		return failure, licensing.ErrRefreshUnavailable
	}
	data, err = jsonobject.Normalize(data, licensing.MaxLicenseBytes+4096)
	if err != nil {
		return failure, licensing.ErrRefreshRejected
	}
	// Exact field names, types and count bind the authenticated response to this
	// request. A cached response cannot extend the confirmation/grace anchor.
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 5 {
		return failure, licensing.ErrRefreshRejected
	}
	var gotOrg, gotID, gotNonce, status string
	for key, dst := range map[string]*string{"organization_id": &gotOrg, "license_id": &gotID, "nonce": &gotNonce, "status": &status} {
		if json.Unmarshal(fields[key], dst) != nil {
			return failure, licensing.ErrRefreshRejected
		}
	}
	if gotOrg != org || gotID != licenseID || gotNonce != nonce {
		return failure, licensing.ErrRefreshRejected
	}
	document, ok := fields["envelope"]
	if !ok {
		return failure, licensing.ErrRefreshRejected
	}
	switch status {
	case "active":
		if len(document) > licensing.MaxLicenseBytes || len(document) < 2 || document[0] != '{' {
			return failure, licensing.ErrRefreshRejected
		}
	case "revoked", "denied":
		if !bytes.Equal(document, []byte("null")) {
			return failure, licensing.ErrRefreshRejected
		}
	default:
		return failure, licensing.ErrRefreshRejected
	}
	return licensing.RefreshResult{Status: status, Envelope: document}, nil
}
