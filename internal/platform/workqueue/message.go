// Package workqueue binds broker subjects to tenant-scoped durable work IDs.
package workqueue

import (
	"encoding/json"
	"errors"

	"github.com/nats-io/nats.go"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/tenant"
)

var ErrInvalid = errors.New("invalid tenant-bound work message")

const Proof = "proof"
const GitHubPR = "github_pr"

type Work struct {
	OrganizationID string `json:"organization_id"`
	ID             string `json:"work_id"`
}

func Subject(kind, org string) (string, error) {
	if (kind != Proof && kind != GitHubPR) || !tenant.ValidID(org) {
		return "", ErrInvalid
	}
	return "safepatch.tenant." + org + "." + kind, nil
}
func Encode(kind, org, id, stream string) (*nats.Msg, error) {
	subject, err := Subject(kind, org)
	if err != nil || !tenant.ValidID(id) || !tenant.ValidID(stream) {
		return nil, ErrInvalid
	}
	raw, _ := json.Marshal(Work{org, id})
	m := nats.NewMsg(subject)
	m.Data = raw
	m.Header.Set("Nats-Msg-Id", org+":"+kind+":"+id)
	m.Header.Set("Nats-Expected-Stream", stream)
	return m, nil
}

// Broker ACLs must restrict tenant publishers to their exact subject prefix.
// Envelope claims cannot change the authenticated subject's tenant. Workers
// still reload the scoped record and current actor permissions before execution.
func Decode(m *nats.Msg, kind string) (Work, error) {
	if m == nil {
		return Work{}, ErrInvalid
	}
	raw, err := jsonobject.Normalize(m.Data, 1024)
	if err != nil {
		return Work{}, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
		return Work{}, ErrInvalid
	}
	var w Work
	if json.Unmarshal(fields["organization_id"], &w.OrganizationID) != nil || json.Unmarshal(fields["work_id"], &w.ID) != nil || !tenant.ValidID(w.ID) {
		return Work{}, ErrInvalid
	}
	subject, err := Subject(kind, w.OrganizationID)
	if err != nil || m.Subject != subject {
		return Work{}, ErrInvalid
	}
	return w, nil
}
