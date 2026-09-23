// Package graphstore implements the shared PostgreSQL EvidenceGraph and ChangeGraph adapter.
package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/graph"
)

// Composition owns primary DB driver registration, TLS, credentials and a fixed
// trusted search_path. Table writers are trusted services, never scanner roles.
type Postgres struct{ db *sql.DB }

var _ graph.EvidenceGraph = (*Postgres)(nil)

func NewPostgres(db *sql.DB) (*Postgres, error) {
	if db == nil {
		return nil, graph.ErrInvalidGraph
	}
	return &Postgres{db}, nil
}
func (s *Postgres) check(ctx context.Context, scope graph.Scope) error {
	if s == nil || s.db == nil || ctx == nil || scope.Validate() != nil {
		return graph.ErrInvalidGraph
	}
	return ctx.Err()
}
func repositories(scope graph.Scope) string {
	raw, _ := json.Marshal(scope.RepositoryIDs)
	return string(raw)
}

func (s *Postgres) PutEvidence(ctx context.Context, scope graph.Scope, repository string, node evidence.Evidence) error {
	if err := s.check(ctx, scope); err != nil {
		return err
	}
	f, err := node.Fields()
	if err != nil || f.TenantID != scope.TenantID || !scope.Contains(repository, f.CreatedAt) {
		return graph.ErrInvalidGraph
	}
	raw, _ := node.CanonicalJSON()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO evidence_graph_nodes (tenant_id,evidence_id,repository_id,content_hash,canonical_evidence,created_at)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, scope.TenantID, f.EvidenceID, repository, f.ContentHash, string(raw), f.CreatedAt.UTC().Truncate(time.Microsecond))
	if err != nil {
		return err
	}
	var stored, repo string
	err = tx.QueryRowContext(ctx, `SELECT canonical_evidence,repository_id FROM evidence_graph_nodes WHERE tenant_id=$1 AND evidence_id=$2`, scope.TenantID, f.EvidenceID).Scan(&stored, &repo)
	if err != nil {
		return err
	}
	if repo != repository || !bytes.Equal([]byte(stored), raw) {
		return graph.ErrConflict
	}
	return tx.Commit()
}

func (s *Postgres) GetEvidence(ctx context.Context, scope graph.Scope, id string) (evidence.Evidence, error) {
	if err := s.check(ctx, scope); err != nil {
		return evidence.Evidence{}, err
	}
	if !evidence.ValidIdentifier(id) {
		return evidence.Evidence{}, graph.ErrInvalidGraph
	}
	var raw, repository string
	err := s.db.QueryRowContext(ctx, `SELECT canonical_evidence,repository_id FROM evidence_graph_nodes
WHERE tenant_id=$1 AND repository_id IN (SELECT jsonb_array_elements_text($2::jsonb)) AND evidence_id=$3
AND created_at >= $4 AND created_at <= $5`, scope.TenantID, repositories(scope), id, scope.NotBefore, scope.AsOf).Scan(&raw, &repository)
	if errors.Is(err, sql.ErrNoRows) {
		return evidence.Evidence{}, graph.ErrNotFound
	}
	if err != nil {
		return evidence.Evidence{}, err
	}
	node, err := evidence.DecodeEvidence([]byte(raw))
	if err != nil {
		return evidence.Evidence{}, graph.ErrGraphData
	}
	f, _ := node.Fields()
	if f.TenantID != scope.TenantID || f.EvidenceID != id || !scope.Contains(repository, f.CreatedAt) {
		return evidence.Evidence{}, graph.ErrGraphData
	}
	return node, nil
}

func (s *Postgres) AddRelationship(ctx context.Context, scope graph.Scope, edge graph.Edge) error {
	if err := s.check(ctx, scope); err != nil {
		return err
	}
	edge, err := edge.Canonical()
	if err != nil || edge.TenantID != scope.TenantID || edge.CreatedAt.Before(scope.NotBefore) || edge.CreatedAt.After(scope.AsOf) {
		return graph.ErrInvalidGraph
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// FK/append-only constraints prevent endpoint disappearance after this check.
	var count int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM evidence_graph_nodes WHERE tenant_id=$1
AND repository_id IN (SELECT jsonb_array_elements_text($2::jsonb)) AND evidence_id IN ($3,$4)
AND created_at >= $5 AND created_at <= $6`, scope.TenantID, repositories(scope), edge.FromEvidenceID, edge.ToEvidenceID, scope.NotBefore, scope.AsOf).Scan(&count)
	if err != nil {
		return err
	}
	if count != 2 {
		return graph.ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO evidence_graph_edges (tenant_id,edge_id,from_evidence_id,to_evidence_id,relationship,created_at)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, edge.TenantID, edge.EdgeID, edge.FromEvidenceID, edge.ToEvidenceID, edge.Relationship, edge.CreatedAt)
	if err != nil {
		return err
	}
	var from, to, relation string
	var created time.Time
	err = tx.QueryRowContext(ctx, `SELECT from_evidence_id,to_evidence_id,relationship,created_at FROM evidence_graph_edges WHERE tenant_id=$1 AND edge_id=$2`, scope.TenantID, edge.EdgeID).Scan(&from, &to, &relation, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return graph.ErrConflict
	}
	if err != nil {
		return err
	}
	if from != edge.FromEvidenceID || to != edge.ToEvidenceID || relation != string(edge.Relationship) || !created.Equal(edge.CreatedAt) {
		return graph.ErrConflict
	}
	return tx.Commit()
}

func (s *Postgres) Neighbors(ctx context.Context, scope graph.Scope, q graph.Query) (graph.Page, error) {
	page := graph.Page{Edges: []graph.Edge{}}
	if err := s.check(ctx, scope); err != nil {
		return page, err
	}
	if err := q.Validate(); err != nil {
		return page, err
	}
	if _, err := s.GetEvidence(ctx, scope, q.EvidenceID); err != nil {
		return page, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.edge_id,e.from_evidence_id,e.to_evidence_id,e.relationship,e.created_at,
f.repository_id,t.repository_id,f.created_at,t.created_at
FROM evidence_graph_edges e
JOIN evidence_graph_nodes f ON (f.tenant_id=e.tenant_id AND f.evidence_id=e.from_evidence_id)
JOIN evidence_graph_nodes t ON (t.tenant_id=e.tenant_id AND t.evidence_id=e.to_evidence_id)
WHERE e.tenant_id=$1 AND f.repository_id IN (SELECT jsonb_array_elements_text($2::jsonb))
AND t.repository_id IN (SELECT jsonb_array_elements_text($2::jsonb))
AND f.created_at BETWEEN $3 AND $4 AND t.created_at BETWEEN $3 AND $4 AND e.created_at BETWEEN $3 AND $4
AND (($6 IN ('OUTGOING','BOTH') AND e.from_evidence_id=$5) OR ($6 IN ('INCOMING','BOTH') AND e.to_evidence_id=$5))
AND ($7='' OR e.relationship=$7) AND e.edge_id COLLATE "C" > $8 COLLATE "C"
ORDER BY e.edge_id COLLATE "C" LIMIT $9`, scope.TenantID, repositories(scope), scope.NotBefore, scope.AsOf, q.EvidenceID, q.Direction, q.Relationship, q.AfterEdgeID, q.Limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	previous := q.AfterEdgeID
	for rows.Next() {
		e := graph.Edge{TenantID: scope.TenantID}
		var fromRepo, toRepo string
		var fromTime, toTime time.Time
		if err := rows.Scan(&e.EdgeID, &e.FromEvidenceID, &e.ToEvidenceID, &e.Relationship, &e.CreatedAt, &fromRepo, &toRepo, &fromTime, &toTime); err != nil {
			return graph.Page{}, err
		}
		matches := (q.Direction == graph.Outgoing || q.Direction == graph.Both) && e.FromEvidenceID == q.EvidenceID || (q.Direction == graph.Incoming || q.Direction == graph.Both) && e.ToEvidenceID == q.EvidenceID
		if e.Validate() != nil || !matches || q.Relationship != "" && q.Relationship != e.Relationship || e.EdgeID <= previous || !scope.Contains(fromRepo, fromTime) || !scope.Contains(toRepo, toTime) || e.CreatedAt.Before(scope.NotBefore) || e.CreatedAt.After(scope.AsOf) || len(page.Edges) > q.Limit {
			return graph.Page{}, graph.ErrGraphData
		}
		previous = e.EdgeID
		page.Edges = append(page.Edges, e)
	}
	if err := rows.Err(); err != nil {
		return graph.Page{}, err
	}
	if len(page.Edges) > q.Limit {
		page.Edges = page.Edges[:q.Limit]
		page.NextCursor = page.Edges[len(page.Edges)-1].EdgeID
	}
	return page, nil
}
