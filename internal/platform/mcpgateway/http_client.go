package mcpgateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"safepatch/internal/agent"
)

const ProtocolVersion = "2025-11-25"

// HTTPClient pins both the HTTPS destination and its operator-approved IPs.
// DNS, proxy environment variables, redirects and request-selected URLs cannot
// change the destination. Private/loopback IPs require explicit deployment pins;
// link-local metadata, unspecified and multicast addresses are always rejected.
type HTTPClient struct {
	endpoint, token string
	client          *http.Client
	mu              sync.Mutex
	ready           bool
	session         string
	sequence        atomic.Uint64
}

func NewHTTPClient(endpoint, token string, pins []netip.Addr) (*HTTPClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || len(endpoint) > 2048 || len(pins) == 0 || len(pins) > 16 || len(token) > 8192 || strings.ContainsAny(token, "\r\n") {
		return nil, agent.ErrInvalid
	}
	for _, ip := range pins {
		if !ip.IsValid() || ip.Zone() != "" || ip.Is4In6() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return nil, agent.ErrInvalid
		}
	}
	// One explicit pin per connection attempt; HTTP never retries tools/call.
	pin := pins[0]
	port := u.Port()
	if port == "" {
		port = "443"
	}
	target := net.JoinHostPort(pin.String(), port)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 16 * 1024, MaxConnsPerHost: 16, MaxIdleConnsPerHost: 4, DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != u.Host && address != net.JoinHostPort(u.Hostname(), port) {
				return nil, agent.ErrDenied
			}
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", target)
		}}
	return &HTTPClient{endpoint: endpoint, token: token, client: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return agent.ErrDenied }}}, nil
}
func (c *HTTPClient) Request(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if c == nil || ctx == nil || (method != "tools/list" && method != "tools/call") {
		return nil, agent.ErrInvalid
	}
	if _, err := agent.CanonicalObject(params, agent.MaxJSONBytes+1024); err != nil {
		return nil, err
	}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	return c.exchange(ctx, method, params, false)
}
func (c *HTTPClient) initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready {
		return nil
	}
	params := json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"SafePatch","version":"1"}}`)
	raw, err := c.exchange(ctx, "initialize", params, false)
	if err != nil {
		return err
	}
	var v struct {
		Protocol     string                     `json:"protocolVersion"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Protocol != ProtocolVersion || len(v.Capabilities["tools"]) == 0 {
		return agent.ErrUnavailable
	}
	if _, err := agent.CanonicalObject(v.Capabilities["tools"], agent.MaxJSONBytes); err != nil {
		return agent.ErrUnavailable
	}
	if _, err = c.exchange(ctx, "notifications/initialized", json.RawMessage(`{}`), true); err != nil {
		return err
	}
	c.ready = true
	return nil
}
func (c *HTTPClient) exchange(ctx context.Context, method string, params json.RawMessage, notification bool) (json.RawMessage, error) {
	id := fmt.Sprintf("sp_%d", c.sequence.Add(1))
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if !notification {
		msg["id"] = id
	}
	raw, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, agent.ErrUnavailable
	}
	// GetBody remains nil to prevent transport retry after an ambiguous write.
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, agent.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, agent.ErrUnavailable
	}
	if method == "initialize" {
		session := response.Header.Get("Mcp-Session-Id")
		if len(session) > 256 {
			return nil, agent.ErrUnavailable
		}
		for _, ch := range session {
			if ch < 33 || ch > 126 {
				return nil, agent.ErrUnavailable
			}
		}
		c.session = session
	}
	if notification {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, agent.MaxInventoryBytes+1))
	if err != nil || len(body) > agent.MaxInventoryBytes {
		return nil, agent.ErrUnavailable
	}
	contentType := response.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/event-stream") {
		// Bounded finite SSE response: accept exactly one JSON-RPC message. No
		// reconnect, event ID resumption, sampling or server-initiated execution.
		var data []byte
		for _, line := range bytes.Split(body, []byte("\n")) {
			line = bytes.TrimSuffix(line, []byte("\r"))
			if bytes.HasPrefix(line, []byte("data:")) {
				if len(data) > 0 {
					return nil, agent.ErrUnavailable
				}
				data = bytes.TrimSpace(line[5:])
			}
		}
		body = data
	} else if !strings.HasPrefix(contentType, "application/json") {
		return nil, agent.ErrUnavailable
	}
	checked, err := agent.CanonicalObject(body, agent.MaxInventoryBytes)
	if err != nil {
		return nil, agent.ErrUnavailable
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(checked, &obj) != nil {
		return nil, agent.ErrUnavailable
	}
	var version, gotID string
	if json.Unmarshal(obj["jsonrpc"], &version) != nil || version != "2.0" || json.Unmarshal(obj["id"], &gotID) != nil || gotID != id || len(obj["error"]) > 0 || len(obj["result"]) == 0 {
		return nil, agent.ErrUnavailable
	}
	result, err := agent.CanonicalObject(obj["result"], agent.MaxInventoryBytes)
	if err != nil {
		return nil, agent.ErrUnavailable
	}
	return result, nil
}
