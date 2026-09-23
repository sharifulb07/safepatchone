package agent

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"safepatch/internal/evidence"
	"safepatch/internal/intent"
)

const MaxCalls = 32
const MaxLeaseSeconds = 600
const MaxCapabilityBytes = 64 * 1024

type Call struct {
	ServerID  string          `json:"server_id"`
	ToolName  string          `json:"tool_name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (c Call) Canonical() (Call, error) {
	if !evidence.ValidIdentifier(c.ServerID) || !evidence.ValidIdentifier(c.ToolName) {
		return Call{}, ErrInvalid
	}
	raw, err := CanonicalObject(c.Arguments, MaxJSONBytes)
	if err != nil {
		return Call{}, err
	}
	c.Arguments = raw
	return c, nil
}
func (c Call) Digest() string {
	v, err := c.Canonical()
	if err != nil {
		return ""
	}
	raw, _ := json.Marshal(v)
	return evidence.Digest(append([]byte("safepatch.agent.call.v1\n"), raw...))
}
func (c Call) ArgumentsDigest() string {
	v, err := c.Canonical()
	if err != nil {
		return ""
	}
	return evidence.Digest(v.Arguments)
}

type Effect string

const Read Effect = "READ"
const Write Effect = "WRITE_NONPRODUCTION"

// Rule is trusted, operator-reviewed configuration for an exact argument object.
// Inventories and LLM proposals cannot add rules, infer effects or approve them.
// Exact argument digests deliberately avoid partial JSON-schema interpretation,
// hidden path/URL fields, wildcard authority and prompt-based authorization.
type Rule struct {
	ServerID        string `json:"server_id"`
	ToolName        string `json:"tool_name"`
	ToolDigest      string `json:"tool_digest"`
	ArgumentsDigest string `json:"arguments_digest"`
	Component       string `json:"component"`
	Effect          Effect `json:"effect"`
}
type Policy struct {
	Scope        Scope  `json:"scope"`
	Revision     int64  `json:"revision"`
	LeaseSeconds uint32 `json:"lease_seconds"`
	CallBudget   uint32 `json:"call_budget"`
	Rules        []Rule `json:"rules"`
}

func (p Policy) Digest() string {
	if p.Scope.Validate() != nil || p.Revision <= 0 || p.LeaseSeconds == 0 || p.LeaseSeconds > MaxLeaseSeconds || p.CallBudget == 0 || p.CallBudget > MaxCalls || len(p.Rules) > MaxTools {
		return ""
	}
	p.Rules = append([]Rule{}, p.Rules...)
	seen := map[string]bool{}
	for _, r := range p.Rules {
		key := r.ServerID + "/" + r.ToolName + "/" + r.ArgumentsDigest
		if !evidence.ValidIdentifier(r.ServerID) || !evidence.ValidIdentifier(r.ToolName) || !validHash(r.ToolDigest) || !validHash(r.ArgumentsDigest) || !evidence.ValidPath(r.Component) || len(r.Component) > intent.MaxComponentBytes || (r.Effect != Read && r.Effect != Write) || seen[key] {
			return ""
		}
		seen[key] = true
	}
	slices.SortFunc(p.Rules, func(a, b Rule) int {
		return strings.Compare(a.ServerID+"/"+a.ToolName+"/"+a.ArgumentsDigest, b.ServerID+"/"+b.ToolName+"/"+b.ArgumentsDigest)
	})
	raw, _ := json.Marshal(p)
	return evidence.Digest(append([]byte("safepatch.agent.policy.v1\n"), raw...))
}

type Grant struct {
	CallDigest string `json:"call_digest"`
	ToolDigest string `json:"tool_digest"`
}
type CapabilityFields struct {
	Schema          string         `json:"schema"`
	Scope           Scope          `json:"scope"`
	IntentDigest    string         `json:"intent_digest"`
	InventoryDigest string         `json:"inventory_digest"`
	PolicyDigest    string         `json:"policy_digest"`
	LeaseSeconds    uint32         `json:"lease_seconds"`
	CallBudget      uint32         `json:"call_budget"`
	Grants          []Grant        `json:"grants"`
	Earned          *EarnedBinding `json:"earned,omitempty"`
}
type Capabilities struct{ canonical string }

// Compile intersects explicit proposals with all three inputs. Natural-language
// intent statements remain hashed evidence, not executable authorization. The
// current compiler requires exact literal component declarations and reviewed
// argument digests. Unknown, production and destructive effects are rejected.
func Compile(contract intent.IntentContract, inv Inventory, p Policy, calls []Call) (Capabilities, error) {
	f, err := contract.Fields()
	if err != nil {
		return Capabilities{}, ErrInvalid
	}
	inventory, err := inv.Fields()
	if err != nil || inventory.Scope != p.Scope || p.Digest() == "" || len(calls) == 0 || len(calls) > MaxCalls {
		return Capabilities{}, ErrInvalid
	}
	intentHash, _ := contract.ContentHash()
	out := CapabilityFields{Schema: "safepatch.agent.capabilities.v1", Scope: p.Scope, IntentDigest: intentHash, InventoryDigest: inv.Digest(), PolicyDigest: p.Digest(), LeaseSeconds: p.LeaseSeconds, CallBudget: p.CallBudget, Grants: []Grant{}}
	for _, call := range calls {
		hash := call.Digest()
		if hash == "" {
			return Capabilities{}, ErrInvalid
		}
		approved := false
		for _, r := range p.Rules {
			if r.ServerID != call.ServerID || r.ToolName != call.ToolName || r.ArgumentsDigest != call.ArgumentsDigest() {
				continue
			}
			forbidden := false
			for _, component := range f.ForbiddenComponents {
				if r.Component == component || strings.HasPrefix(r.Component, component+"/") {
					forbidden = true
				}
			}
			if !slices.Contains(f.AllowedComponents, r.Component) || forbidden {
				return Capabilities{}, ErrDenied
			}
			for _, tool := range inventory.Tools {
				if tool.ServerID == r.ServerID && tool.Name == r.ToolName && tool.Digest() == r.ToolDigest {
					out.Grants = append(out.Grants, Grant{CallDigest: hash, ToolDigest: r.ToolDigest})
					approved = true
				}
			}
		}
		if !approved {
			return Capabilities{}, ErrDenied
		}
	}
	return sealCapabilities(out)
}
func sealCapabilities(f CapabilityFields) (Capabilities, error) {
	if f.Earned != nil && !f.Earned.valid() {
		return Capabilities{}, ErrInvalid
	}
	if f.Schema != "safepatch.agent.capabilities.v1" || f.Scope.Validate() != nil || !validHash(f.IntentDigest) || !validHash(f.InventoryDigest) || !validHash(f.PolicyDigest) || f.LeaseSeconds == 0 || f.LeaseSeconds > MaxLeaseSeconds || f.CallBudget == 0 || f.CallBudget > MaxCalls || len(f.Grants) == 0 || len(f.Grants) > MaxCalls {
		return Capabilities{}, ErrInvalid
	}
	f.Grants = append([]Grant{}, f.Grants...)
	slices.SortFunc(f.Grants, func(a, b Grant) int { return strings.Compare(a.CallDigest, b.CallDigest) })
	for n, g := range f.Grants {
		if !validHash(g.CallDigest) || !validHash(g.ToolDigest) || n > 0 && g.CallDigest == f.Grants[n-1].CallDigest {
			return Capabilities{}, ErrInvalid
		}
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxCapabilityBytes {
		return Capabilities{}, ErrInvalid
	}
	return Capabilities{string(raw)}, nil
}
func (c Capabilities) Fields() (CapabilityFields, error) {
	var f CapabilityFields
	if c.canonical == "" || json.Unmarshal([]byte(c.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (c Capabilities) MarshalJSON() ([]byte, error) {
	if c.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(c.canonical), nil
}
func (*Capabilities) UnmarshalJSON([]byte) error { return ErrInvalid }
func (c Capabilities) Digest() string {
	if c.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(c.canonical))
}
func DecodeCapabilities(raw []byte) (Capabilities, error) {
	checked, err := CanonicalObject(raw, MaxCapabilityBytes)
	if err != nil {
		return Capabilities{}, err
	}
	var f CapabilityFields
	if json.Unmarshal(checked, &f) != nil {
		return Capabilities{}, ErrInvalid
	}
	c, err := sealCapabilities(f)
	if err != nil {
		return Capabilities{}, err
	}
	want, _ := CanonicalObject([]byte(c.canonical), MaxCapabilityBytes)
	if !bytes.Equal(checked, want) {
		return Capabilities{}, ErrInvalid
	}
	return c, nil
}
