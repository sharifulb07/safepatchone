// Package mcpgateway adapts MCP JSON-RPC to the shared agent gateway.
package mcpgateway

import (
	"context"
	"encoding/json"
	"slices"

	"safepatch/internal/agent"
	"safepatch/internal/evidence"
)

type RPC interface {
	Request(context.Context, string, json.RawMessage) (json.RawMessage, error)
}
type Binding struct {
	Scope    agent.Scope
	ServerID string
	Client   RPC
}
type Runtime struct{ bindings map[agent.Scope][]Binding }

func NewRuntime(bindings []Binding) (*Runtime, error) {
	if len(bindings) == 0 || len(bindings) > 4096 {
		return nil, agent.ErrInvalid
	}
	r := &Runtime{bindings: map[agent.Scope][]Binding{}}
	for _, b := range bindings {
		if b.Scope.Validate() != nil || !evidence.ValidIdentifier(b.ServerID) || b.Client == nil || len(r.bindings[b.Scope]) >= 16 {
			return nil, agent.ErrInvalid
		}
		for _, old := range r.bindings[b.Scope] {
			if old.ServerID == b.ServerID {
				return nil, agent.ErrInvalid
			}
		}
		r.bindings[b.Scope] = append(r.bindings[b.Scope], b)
	}
	return r, nil
}
func (r *Runtime) Inventory(ctx context.Context, scope agent.Scope) (agent.Inventory, error) {
	if r == nil || ctx == nil || scope.Validate() != nil || len(r.bindings[scope]) == 0 {
		return agent.Inventory{}, agent.ErrDenied
	}
	tools := []agent.Tool{}
	total := 0
	for _, b := range r.bindings[scope] {
		cursor := ""
		seen := map[string]bool{}
		for page := 0; page < 16; page++ {
			params := json.RawMessage(`{}`)
			if cursor != "" {
				params, _ = json.Marshal(map[string]string{"cursor": cursor})
			}
			raw, err := b.Client.Request(ctx, "tools/list", params)
			if err != nil || ctx.Err() != nil {
				return agent.Inventory{}, agent.ErrUnavailable
			}
			total += len(raw)
			if total > agent.MaxInventoryBytes {
				return agent.Inventory{}, agent.ErrInvalid
			}
			checked, err := agent.CanonicalObject(raw, agent.MaxInventoryBytes)
			if err != nil {
				return agent.Inventory{}, err
			}
			var obj map[string]json.RawMessage
			if json.Unmarshal(checked, &obj) != nil {
				return agent.Inventory{}, agent.ErrInvalid
			}
			var rows []json.RawMessage
			if json.Unmarshal(obj["tools"], &rows) != nil || rows == nil || len(tools)+len(rows) > agent.MaxTools {
				return agent.Inventory{}, agent.ErrInvalid
			}
			for _, row := range rows {
				var tool struct {
					Name string `json:"name"`
				}
				if json.Unmarshal(row, &tool) != nil {
					return agent.Inventory{}, agent.ErrInvalid
				}
				tools = append(tools, agent.Tool{ServerID: b.ServerID, Name: tool.Name, Definition: row})
			}
			cursor = ""
			if value, ok := obj["nextCursor"]; ok {
				if json.Unmarshal(value, &cursor) != nil || cursor == "" || len(cursor) > 256 {
					return agent.Inventory{}, agent.ErrInvalid
				}
			}
			if cursor == "" {
				break
			}
			if seen[cursor] || page == 15 {
				return agent.Inventory{}, agent.ErrInvalid
			}
			seen[cursor] = true
		}
	}
	return agent.NewInventory(scope, tools)
}
func (r *Runtime) Call(ctx context.Context, scope agent.Scope, call agent.Call) (json.RawMessage, error) {
	if r == nil || ctx == nil || scope.Validate() != nil {
		return nil, agent.ErrInvalid
	}
	call, err := call.Canonical()
	if err != nil {
		return nil, err
	}
	for _, b := range r.bindings[scope] {
		if b.ServerID == call.ServerID {
			params, _ := json.Marshal(struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}{call.ToolName, call.Arguments})
			return b.Client.Request(ctx, "tools/call", params)
		}
	}
	return nil, agent.ErrDenied
}

// Policies freezes operator-owned exact-call policies at composition. A reload
// constructs a new source with a higher revision; existing lease digests then
// fail. No edition names or tool annotations influence these rules.
type Policies struct{ rows map[agent.Scope]agent.Policy }

func NewPolicies(rows []agent.Policy) (*Policies, error) {
	if len(rows) == 0 || len(rows) > 4096 {
		return nil, agent.ErrInvalid
	}
	p := &Policies{rows: map[agent.Scope]agent.Policy{}}
	for _, row := range rows {
		if row.Digest() == "" || p.rows[row.Scope].Revision != 0 {
			return nil, agent.ErrInvalid
		}
		row.Rules = slices.Clone(row.Rules)
		p.rows[row.Scope] = row
	}
	return p, nil
}
func (p *Policies) Policy(ctx context.Context, scope agent.Scope) (agent.Policy, error) {
	if p == nil || ctx == nil || ctx.Err() != nil {
		return agent.Policy{}, agent.ErrUnavailable
	}
	row, ok := p.rows[scope]
	if !ok {
		return agent.Policy{}, agent.ErrDenied
	}
	row.Rules = slices.Clone(row.Rules)
	return row, nil
}
