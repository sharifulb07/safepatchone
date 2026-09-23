package sir

import (
	"bytes"
	"context"
	"encoding/json"

	"safepatch/internal/domain/jsonobject"
)

// Decode requires the exact versioned schema, including explicit empty fields
// and arrays. It rejects duplicate/case-alias/unknown keys, nulls, invalid Unicode,
// trailing documents and oversized input before sealing validated facts.
func Decode(ctx context.Context, raw []byte) (Representation, error) {
	if err := checkContext(ctx); err != nil {
		return Representation{}, err
	}
	checked, err := jsonobject.Normalize(raw, MaxBytes)
	if err != nil {
		return Representation{}, ErrInvalidRepresentation
	}
	f, err := exact(checked, "schema", "tenant_id", "subject", "producer", "producer_version", "deterministic", "coverage", "files", "nodes")
	if err != nil {
		return Representation{}, err
	}
	if _, err := exact(f["subject"], "repository_id", "revision"); err != nil {
		return Representation{}, err
	}
	var files, nodes []json.RawMessage
	if json.Unmarshal(f["files"], &files) != nil || len(files) > MaxFiles || json.Unmarshal(f["nodes"], &nodes) != nil || len(nodes) > MaxNodes {
		return Representation{}, ErrInvalidRepresentation
	}
	for _, file := range files {
		if _, err := exact(file, "path", "content_hash"); err != nil {
			return Representation{}, err
		}
	}
	for _, node := range nodes {
		if err := checkContext(ctx); err != nil {
			return Representation{}, err
		}
		n, err := exact(node, "id", "language", "native_kind", "name", "source", "parent_id", "target_id", "target", "action", "kind")
		if err != nil {
			return Representation{}, err
		}
		if _, err := exact(n["source"], "path", "start_line", "start_column", "end_line", "end_column"); err != nil {
			return Representation{}, err
		}
	}
	var fields Fields
	if json.Unmarshal(checked, &fields) != nil {
		return Representation{}, ErrInvalidRepresentation
	}
	return New(ctx, fields)
}

func exact(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != len(keys) {
		return nil, ErrInvalidRepresentation
	}
	for _, key := range keys {
		v, ok := fields[key]
		if !ok || bytes.Equal(v, []byte("null")) {
			return nil, ErrInvalidRepresentation
		}
	}
	return fields, nil
}
