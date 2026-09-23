package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
)

const MaxTools = 128
const MaxJSONBytes = 16 * 1024
const MaxInventoryBytes = 1024 * 1024

var ErrInvalid = errors.New("invalid agent configuration or request")
var ErrDenied = errors.New("agent operation denied")
var ErrReplay = errors.New("agent invocation already consumed")
var ErrUnavailable = errors.New("agent service unavailable")

type Scope struct {
	OrganizationID string `json:"organization_id"`
	RepositoryID   string `json:"repository_id"`
}

func (s Scope) Tenant() tenant.Scope {
	return tenant.Scope{OrganizationID: s.OrganizationID, ResourceID: s.RepositoryID}
}
func (s Scope) Validate() error { return s.Tenant().Validate() }

// CanonicalObject rejects ambiguous untrusted JSON without evaluating schemas,
// following references, coercing large numbers or interpreting prompt text.
func CanonicalObject(raw []byte, bound int) ([]byte, error) {
	checked, err := jsonobject.Normalize(raw, bound)
	if err != nil {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(checked))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil, ErrInvalid
	}
	return json.Marshal(v)
}
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type Tool struct {
	ServerID   string          `json:"server_id"`
	Name       string          `json:"name"`
	Definition json.RawMessage `json:"definition"`
}

func (t Tool) Digest() string {
	if !evidence.ValidIdentifier(t.ServerID) || !evidence.ValidIdentifier(t.Name) {
		return ""
	}
	raw, err := CanonicalObject(t.Definition, MaxJSONBytes)
	if err != nil {
		return ""
	}
	return evidence.Digest(append([]byte("safepatch.mcp.tool.v1\n"+t.ServerID+"\n"+t.Name+"\n"), raw...))
}

type InventoryFields struct {
	Schema string `json:"schema"`
	Scope  Scope  `json:"scope"`
	Tools  []Tool `json:"tools"`
}
type Inventory struct{ canonical string }

func NewInventory(scope Scope, tools []Tool) (Inventory, error) {
	if scope.Validate() != nil || len(tools) > MaxTools {
		return Inventory{}, ErrInvalid
	}
	f := InventoryFields{Schema: "safepatch.mcp.inventory.v1", Scope: scope, Tools: []Tool{}}
	seen := map[string]bool{}
	total := 0
	for _, t := range tools {
		if !evidence.ValidIdentifier(t.ServerID) || !evidence.ValidIdentifier(t.Name) || seen[t.ServerID+"/"+t.Name] {
			return Inventory{}, ErrInvalid
		}
		total += len(t.Definition)
		if total > MaxInventoryBytes {
			return Inventory{}, ErrInvalid
		}
		raw, err := CanonicalObject(t.Definition, MaxJSONBytes)
		if err != nil {
			return Inventory{}, err
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return Inventory{}, ErrInvalid
		}
		var name string
		if json.Unmarshal(obj["name"], &name) != nil || name != t.Name {
			return Inventory{}, ErrInvalid
		}
		if len(obj["inputSchema"]) == 0 {
			return Inventory{}, ErrInvalid
		}
		if _, err := CanonicalObject(obj["inputSchema"], MaxJSONBytes); err != nil {
			return Inventory{}, ErrInvalid
		}
		// Descriptions, annotations, schemas, icons and output metadata remain inert.
		t.Definition = raw
		seen[t.ServerID+"/"+t.Name] = true
		f.Tools = append(f.Tools, t)
	}
	slices.SortFunc(f.Tools, func(a, b Tool) int { return strings.Compare(a.ServerID+"/"+a.Name, b.ServerID+"/"+b.Name) })
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxInventoryBytes {
		return Inventory{}, ErrInvalid
	}
	return Inventory{string(raw)}, nil
}
func (i Inventory) Fields() (InventoryFields, error) {
	var f InventoryFields
	if i.canonical == "" || json.Unmarshal([]byte(i.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (i Inventory) MarshalJSON() ([]byte, error) {
	if i.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(i.canonical), nil
}
func (*Inventory) UnmarshalJSON([]byte) error { return ErrInvalid }
func (i Inventory) Digest() string {
	if i.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(i.canonical))
}
func DecodeInventory(raw []byte) (Inventory, error) {
	checked, err := CanonicalObject(raw, MaxInventoryBytes)
	if err != nil {
		return Inventory{}, err
	}
	var f InventoryFields
	if json.Unmarshal(checked, &f) != nil {
		return Inventory{}, ErrInvalid
	}
	i, err := NewInventory(f.Scope, f.Tools)
	if err != nil {
		return Inventory{}, err
	}
	want, _ := CanonicalObject([]byte(i.canonical), MaxInventoryBytes)
	if !bytes.Equal(want, checked) {
		return Inventory{}, ErrInvalid
	}
	return i, nil
}
