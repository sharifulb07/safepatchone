package mcpgateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/netip"
	"time"

	"safepatch/internal/agent"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/agentstore"
)

type ServerConfig struct {
	Scope     agent.Scope `json:"scope"`
	ServerID  string      `json:"server_id"`
	Endpoint  string      `json:"endpoint"`
	PinnedIPs []string    `json:"pinned_ips"`
	TokenEnv  string      `json:"token_env"`
}
type Configuration struct {
	Servers  []ServerConfig `json:"servers"`
	Policies []agent.Policy `json:"policies"`
}

// Calls are bounded by each lease's finite call budget, expiry and transport
// limits. No new commercial usage unit is invented or charged to proof credits.
type boundedAgentQuota struct{}

func (boundedAgentQuota) Check(ctx context.Context, _ application.Execution) error { return ctx.Err() }

func NewConfigured(raw []byte, db *sql.DB, tenants application.TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, auth application.Authenticator, secret func(string) string) (*Handler, error) {
	checked, err := agent.CanonicalObject(raw, agent.MaxInventoryBytes)
	if err != nil || secret == nil {
		return nil, agent.ErrInvalid
	}
	var config Configuration
	if json.Unmarshal(checked, &config) != nil || len(config.Servers) == 0 || len(config.Servers) > 4096 {
		return nil, agent.ErrInvalid
	}
	// Operator configuration also rejects misspelled/unknown/aliased fields.
	canonical, _ := json.Marshal(config)
	expected, _ := agent.CanonicalObject(canonical, agent.MaxInventoryBytes)
	if string(expected) != string(checked) {
		return nil, agent.ErrInvalid
	}
	bindings := []Binding{}
	for _, s := range config.Servers {
		ips := []netip.Addr{}
		for _, raw := range s.PinnedIPs {
			ip, e := netip.ParseAddr(raw)
			if e != nil {
				return nil, agent.ErrInvalid
			}
			ips = append(ips, ip)
		}
		token := ""
		if s.TokenEnv != "" {
			if len(s.TokenEnv) > 128 {
				return nil, agent.ErrInvalid
			}
			token = secret(s.TokenEnv)
			if token == "" {
				return nil, agent.ErrInvalid
			}
		}
		client, e := NewHTTPClient(s.Endpoint, token, ips)
		if e != nil {
			return nil, e
		}
		bindings = append(bindings, Binding{Scope: s.Scope, ServerID: s.ServerID, Client: client})
	}
	runtime, err := NewRuntime(bindings)
	if err != nil {
		return nil, err
	}
	policies, err := NewPolicies(config.Policies)
	if err != nil {
		return nil, err
	}
	store, err := agentstore.NewPostgres(db)
	if err != nil {
		return nil, err
	}
	service, err := application.NewAgentGateway(tenants, grants, permissions, boundedAgentQuota{}, policies, runtime, store, time.Now, agentstore.NewID)
	if err != nil {
		return nil, err
	}
	return NewHandler(service, auth)
}
