package mcpgateway

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
)

func TestHTTPMCPHandshakePinnedTransportAndFailurePaths(t *testing.T) {
	for _, mode := range []string{"json", "sse", "redirect", "wrong_id", "protocol", "rpc_error", "bad_json", "too_large", "stream_requests"} {
		t.Run(mode, func(t *testing.T) {
			var initialized, called atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer private-token" || r.Method != "POST" || r.Header.Get("MCP-Protocol-Version") != ProtocolVersion {
					t.Error("headers")
				}
				var request struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("request")
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if request.Method == "initialize" {
					initialized.Add(1)
					protocol := ProtocolVersion
					if mode == "protocol" {
						protocol = "unsupported"
					}
					w.Header().Set("Mcp-Session-Id", "session")
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": protocol, "capabilities": map[string]any{"tools": map[string]any{}}}})
					return
				}
				if r.Header.Get("Mcp-Session-Id") != "session" {
					t.Error("missing session")
				}
				if request.Method == "notifications/initialized" {
					w.WriteHeader(202)
					return
				}
				called.Add(1)
				if mode == "redirect" {
					w.Header().Set("Location", "https://example.invalid/steal")
					w.WriteHeader(307)
					return
				}
				if mode == "wrong_id" {
					request.ID = "wrong"
				}
				if mode == "rpc_error" {
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"error":{"code":-1,"message":"secret"}}`, request.ID)
					return
				}
				if mode == "bad_json" {
					fmt.Fprint(w, `{"jsonrpc":"2.0","jsonrpc":"2.0"}`)
					return
				}
				if mode == "too_large" {
					_, _ = w.Write(make([]byte, 1024*1024+1))
					return
				}
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": []any{}}})
				if mode == "sse" || mode == "stream_requests" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
					if mode == "stream_requests" {
						fmt.Fprint(w, "data: {\"method\":\"sampling/createMessage\"}\n\n")
					}
					return
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client, err := NewHTTPClient(server.URL, "private-token", []netip.Addr{netip.MustParseAddr("127.0.0.1")})
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			client.client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
			_, err = client.Request(t.Context(), "tools/list", json.RawMessage(`{}`))
			if mode == "json" || mode == "sse" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = client.Request(t.Context(), "tools/list", json.RawMessage(`{}`)); err != nil || initialized.Load() != 1 || called.Load() != 2 {
					t.Fatal("session lifecycle", err)
				}
			} else if err == nil {
				t.Fatal("unsafe response accepted")
			}
			if mode == "redirect" && called.Load() != 1 {
				t.Fatal("retried redirected call")
			}
		})
	}
}
func TestHTTPMCPRejectsUnsafeConfiguration(t *testing.T) {
	for _, endpoint := range []string{"http://example.test/mcp", "https://user:password@example.test/mcp", "https://example.test/mcp?token=x", "https://example.test/mcp#fragment", "file:///secret"} {
		if _, err := NewHTTPClient(endpoint, "", []netip.Addr{netip.MustParseAddr("192.0.2.1")}); err == nil {
			t.Fatal(endpoint)
		}
	}
	for _, address := range []string{"169.254.169.254", "0.0.0.0", "224.0.0.1", "fe80::1", "::"} {
		if _, err := NewHTTPClient("https://example.test/mcp", "", []netip.Addr{netip.MustParseAddr(address)}); err == nil {
			t.Fatal(address)
		}
	}
	if _, err := NewHTTPClient("https://example.test/mcp", "secret\r\nInjected: yes", []netip.Addr{netip.MustParseAddr("192.0.2.1")}); err == nil {
		t.Fatal("header injection")
	}
	client, _ := NewHTTPClient("https://example.test/mcp", "", []netip.Addr{netip.MustParseAddr("192.0.2.1")})
	if _, err := client.client.Transport.(*http.Transport).DialContext(context.Background(), "tcp", "other.test:443"); err == nil {
		t.Fatal("unapproved destination")
	}
}
