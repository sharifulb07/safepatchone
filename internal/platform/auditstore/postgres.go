// Package auditstore persists the shared immutable audit stream for all editions.
package auditstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"safepatch/internal/audit"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/tenant"
)

type Postgres struct{ db *sql.DB }

func NewPostgres(db *sql.DB) (*Postgres, error) {
	if db == nil {
		return nil, audit.ErrInvalid
	}
	return &Postgres{db}, nil
}
func (s *Postgres) check(ctx context.Context, org string) error {
	if s == nil || s.db == nil || ctx == nil || !tenant.ValidID(org) {
		return audit.ErrInvalid
	}
	return ctx.Err()
}
func (s *Postgres) Append(ctx context.Context, org string, event audit.Event) error {
	if err := s.check(ctx, org); err != nil {
		return err
	}
	if event.Validate() != nil || event.OrganizationID != org {
		return audit.ErrInvalid
	}
	event.OccurredAt = event.OccurredAt.UTC().Truncate(time.Microsecond)
	raw, err := json.Marshal(event)
	if err != nil || len(raw) > 8192 {
		return audit.ErrInvalid
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_events(organization_id,event_id,record) VALUES($1,$2,$3)`, org, event.ID, string(raw))
	if err != nil {
		var state interface{ SQLState() string }
		if errors.As(err, &state) && state.SQLState() == "23505" {
			return audit.ErrConflict
		}
		return audit.ErrUnavailable
	}
	return nil
}
func (s *Postgres) List(ctx context.Context, org string, q audit.Query) (audit.Page, error) {
	fail := audit.Page{}
	if err := s.check(ctx, org); err != nil {
		return fail, err
	}
	if q.Validate() != nil {
		return fail, audit.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_id,record FROM audit_events WHERE organization_id=$1 AND event_id COLLATE "C">$2 COLLATE "C" ORDER BY event_id COLLATE "C" LIMIT $3`, org, q.AfterID, q.Limit+1)
	if err != nil {
		return fail, audit.ErrUnavailable
	}
	defer rows.Close()
	p := audit.Page{Events: []audit.Event{}}
	previous := q.AfterID
	for rows.Next() {
		var id, raw string
		if rows.Scan(&id, &raw) != nil {
			return fail, audit.ErrUnavailable
		}
		clean, err := jsonobject.Normalize([]byte(raw), 8192)
		if err != nil {
			return fail, audit.ErrInvalid
		}
		var event audit.Event
		decoder := json.NewDecoder(bytes.NewReader(clean))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&event) != nil || event.Validate() != nil || event.OrganizationID != org || event.ID != id || id <= previous {
			return fail, audit.ErrInvalid
		}
		if len(p.Events) == q.Limit {
			p.NextAfterID = previous
			break
		}
		p.Events = append(p.Events, event)
		previous = id
	}
	if rows.Err() != nil {
		return fail, audit.ErrUnavailable
	}
	return p, nil
}
