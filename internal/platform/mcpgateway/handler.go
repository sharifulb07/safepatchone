package mcpgateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"safepatch/internal/agent"
	"safepatch/internal/application"
	"safepatch/internal/entitlement"
	"safepatch/internal/intent"
)

// Handler exposes the shared gateway through organization-scoped HTTP routes.
// Authentication is injected from the existing server, never read from body
// actor/role claims. Bearer authentication is mandatory; cookies are not used.
type Handler struct {
	service *application.AgentGateway
	auth    application.Authenticator
	slots   chan struct{}
}

func NewHandler(service *application.AgentGateway, auth application.Authenticator) (*Handler, error) {
	if service == nil || auth == nil {
		return nil, agent.ErrInvalid
	}
	return &Handler{service: service, auth: auth, slots: make(chan struct{}, 64)}, nil
}
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("POST /v1/organizations/{organization}/repositories/{repository}/agent/{operation}", h)
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	deny := func(status int) { http.Error(w, "agent request unavailable or denied", status) }
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		deny(http.StatusTooManyRequests)
		return
	}
	if r.Method != http.MethodPost {
		deny(http.StatusMethodNotAllowed)
		return
	}
	header := r.Header.Get("Authorization")
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(header, "Bearer ") || len(header) > 16384 {
		deny(http.StatusUnauthorized)
		return
	}
	actor, err := h.auth.Authenticate(r.Context(), strings.TrimPrefix(header, "Bearer "))
	if err != nil || actor.Validate() != nil {
		deny(http.StatusUnauthorized)
		return
	}
	scope := agent.Scope{OrganizationID: r.PathValue("organization"), RepositoryID: r.PathValue("repository")}
	if scope.Validate() != nil {
		deny(http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		deny(http.StatusRequestEntityTooLarge)
		return
	}
	checked, err := agent.CanonicalObject(raw, 128*1024)
	if err != nil {
		deny(http.StatusBadRequest)
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(checked, &fields) != nil {
		deny(http.StatusBadRequest)
		return
	}
	exact := func(keys ...string) bool {
		if len(fields) != len(keys) {
			return false
		}
		for _, k := range keys {
			if len(fields[k]) == 0 || string(fields[k]) == "null" {
				return false
			}
		}
		return true
	}
	var value any
	switch r.PathValue("operation") {
	case "inventory":
		if !exact() {
			deny(400)
			return
		}
		value, err = h.service.Inventory(r.Context(), actor, scope)
	case "compile", "leases":
		if !exact("intent", "calls") {
			deny(400)
			return
		}
		contract, e := intent.DecodeIntentContract(fields["intent"])
		if e != nil {
			deny(400)
			return
		}
		calls, e := decodeCalls(fields["calls"])
		if e != nil {
			deny(400)
			return
		}
		if r.PathValue("operation") == "compile" {
			value, err = h.service.Compile(r.Context(), actor, scope, contract, calls)
		} else {
			lease, e := h.service.Issue(r.Context(), actor, scope, contract, calls)
			err = e
			if err == nil {
				value = map[string]any{"lease_id": lease.ID, "expires_at": lease.ExpiresAt, "capabilities_digest": lease.Capabilities.Digest()}
			}
		}
	case "invoke":
		if !exact("lease_id", "invocation_id", "call") {
			deny(400)
			return
		}
		var id, invocation string
		if json.Unmarshal(fields["lease_id"], &id) != nil || json.Unmarshal(fields["invocation_id"], &invocation) != nil {
			deny(400)
			return
		}
		calls, e := decodeCalls(append(append([]byte{'['}, fields["call"]...), ']'))
		if e != nil || len(calls) != 1 {
			deny(400)
			return
		}
		value, err = h.service.Invoke(r.Context(), actor, scope, id, invocation, calls[0])
	case "revoke":
		if !exact("lease_id") {
			deny(400)
			return
		}
		var id string
		if json.Unmarshal(fields["lease_id"], &id) != nil {
			deny(400)
			return
		}
		err = h.service.Revoke(r.Context(), actor, scope, id)
		value = map[string]bool{"revoked": true}
	default:
		deny(404)
		return
	}
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, agent.ErrInvalid) {
			status = 400
		}
		if errors.Is(err, agent.ErrDenied) || errors.Is(err, application.ErrPermissionDenied) || errors.Is(err, entitlement.ErrNotEntitled) {
			status = 403
		}
		deny(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func decodeCalls(raw []byte) ([]agent.Call, error) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > agent.MaxCalls {
		return nil, agent.ErrInvalid
	}
	calls := []agent.Call{}
	for _, row := range rows {
		if len(row) != 3 || len(row["server_id"]) == 0 || len(row["tool_name"]) == 0 || len(row["arguments"]) == 0 || string(row["arguments"]) == "null" {
			return nil, agent.ErrInvalid
		}
		var call agent.Call
		if json.Unmarshal(row["server_id"], &call.ServerID) != nil || json.Unmarshal(row["tool_name"], &call.ToolName) != nil {
			return nil, agent.ErrInvalid
		}
		call.Arguments = row["arguments"]
		c, err := call.Canonical()
		if err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, nil
}
