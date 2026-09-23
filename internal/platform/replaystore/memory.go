package replaystore

import (
	"context"
	"sync"

	"safepatch/internal/replay"
	"safepatch/internal/tenant"
)

// Memory is bounded local/test storage. Multi-process deployments use Store's
// PostgreSQL claims and immutable object storage instead of process-local state.
type Memory struct {
	mu       sync.Mutex
	datasets map[string]replay.ReplayDataset
	runs     map[string]memoryRun
}
type memoryRun struct {
	digest   string
	finished bool
}

func NewMemory() *Memory {
	return &Memory{datasets: map[string]replay.ReplayDataset{}, runs: map[string]memoryRun{}}
}
func key(scope replay.Scope, id string) string {
	return scope.OrganizationID + "/" + scope.RepositoryID + "/" + id
}
func (m *Memory) Put(ctx context.Context, d replay.ReplayDataset) error {
	f, e := d.Fields()
	if m == nil || ctx == nil || ctx.Err() != nil || e != nil {
		return replay.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(f.Scope, f.ID)
	if _, ok := m.datasets[k]; ok || len(m.datasets) >= 4096 {
		return replay.ErrDenied
	}
	m.datasets[k] = d
	return nil
}
func (m *Memory) Get(ctx context.Context, s replay.Scope, id string) (replay.ReplayDataset, error) {
	if m == nil || ctx == nil || ctx.Err() != nil || s.Validate() != nil || !tenant.ValidID(id) {
		return replay.ReplayDataset{}, replay.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.datasets[key(s, id)]
	if !ok {
		return d, replay.ErrDenied
	}
	return d, nil
}
func (m *Memory) Claim(ctx context.Context, s replay.Scope, id, digest string) error {
	if m == nil || ctx == nil || ctx.Err() != nil || s.Validate() != nil || !tenant.ValidID(id) {
		return replay.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(s, id)
	if _, ok := m.runs[k]; ok || len(m.runs) >= 4096 {
		return replay.ErrDenied
	}
	found := false
	for _, d := range m.datasets {
		f, _ := d.Fields()
		if f.Scope == s && d.Digest() == digest {
			found = true
		}
	}
	if !found {
		return replay.ErrDenied
	}
	m.runs[k] = memoryRun{digest: digest}
	return nil
}
func (m *Memory) Finish(ctx context.Context, s replay.Scope, id string, r replay.Result) error {
	if m == nil || ctx == nil || ctx.Err() != nil {
		return replay.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(s, id)
	v, ok := m.runs[k]
	if !ok || v.finished || v.digest != r.DatasetDigest {
		return replay.ErrDenied
	}
	v.finished = true
	m.runs[k] = v
	return nil
}
