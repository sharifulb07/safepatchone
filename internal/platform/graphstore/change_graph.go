package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"safepatch/internal/graph"
)

var _ graph.ChangeGraph = (*Postgres)(nil)

func (s *Postgres) checkRevision(ctx context.Context, scope graph.RevisionScope) error {
	if err := s.check(ctx, scope.Access); err != nil {
		return err
	}
	return scope.Validate()
}
func pins(scope graph.RevisionScope) string {
	raw, _ := json.Marshal(scope.Revisions)
	return string(raw)
}

func (s *Postgres) PutChangeNode(ctx context.Context, scope graph.RevisionScope, node graph.ChangeNode) error {
	if err := s.checkRevision(ctx, scope); err != nil {
		return err
	}
	f, err := node.Fields()
	if err != nil || f.TenantID != scope.Access.TenantID || !scope.Includes(f.Ref, f.CreatedAt) {
		return graph.ErrInvalidGraph
	}
	raw, _ := node.MarshalJSON()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO causal_change_nodes (tenant_id,repository_id,revision,entity_id,kind,content_hash,canonical_node,created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, f.TenantID, f.Ref.RepositoryID, f.Ref.Revision, f.Ref.EntityID, string(f.Kind), node.ContentHash(), string(raw), f.CreatedAt)
	if err != nil {
		return err
	}
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT canonical_node FROM causal_change_nodes WHERE tenant_id=$1 AND repository_id=$2 AND revision=$3 AND entity_id=$4`, f.TenantID, f.Ref.RepositoryID, f.Ref.Revision, f.Ref.EntityID).Scan(&stored)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, []byte(stored)) {
		return graph.ErrConflict
	}
	return tx.Commit()
}

func (s *Postgres) GetChangeNode(ctx context.Context, scope graph.RevisionScope, ref graph.EntityRef) (graph.ChangeNode, error) {
	if err := s.checkRevision(ctx, scope); err != nil {
		return graph.ChangeNode{}, err
	}
	if ref.Validate() != nil || !scope.Includes(ref, scope.Access.AsOf) {
		return graph.ChangeNode{}, graph.ErrNotFound
	}
	var raw, hash, kind string
	var created time.Time
	err := s.db.QueryRowContext(ctx, `SELECT canonical_node,content_hash,kind,created_at FROM causal_change_nodes
WHERE tenant_id=$1 AND repository_id=$2 AND revision=$3 AND entity_id=$4 AND created_at BETWEEN $5 AND $6`, scope.Access.TenantID, ref.RepositoryID, ref.Revision, ref.EntityID, scope.Access.NotBefore, scope.Access.AsOf).Scan(&raw, &hash, &kind, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return graph.ChangeNode{}, graph.ErrNotFound
	}
	if err != nil {
		return graph.ChangeNode{}, err
	}
	n, err := graph.DecodeChangeNode([]byte(raw))
	if err != nil {
		return graph.ChangeNode{}, graph.ErrGraphData
	}
	f, _ := n.Fields()
	if n.ContentHash() != hash || f.Ref != ref || f.TenantID != scope.Access.TenantID || string(f.Kind) != kind || !f.CreatedAt.Equal(created) || !scope.Includes(ref, f.CreatedAt) {
		return graph.ChangeNode{}, graph.ErrGraphData
	}
	return n, nil
}

func (s *Postgres) PutChangeLink(ctx context.Context, scope graph.RevisionScope, link graph.ChangeLink) error {
	if err := s.checkRevision(ctx, scope); err != nil {
		return err
	}
	f, err := link.Fields()
	if err != nil || f.TenantID != scope.Access.TenantID || !scope.Includes(f.From, f.CreatedAt) || !scope.Includes(f.To, f.CreatedAt) {
		return graph.ErrInvalidGraph
	}
	// Reuse existing immutable evidence validation and tenant/retention filtering.
	source, err := s.GetEvidence(ctx, scope.Access, f.EvidenceID)
	if err != nil {
		return err
	}
	sourceFields, _ := source.Fields()
	if sourceFields.CreatedAt.Truncate(time.Microsecond).After(f.CreatedAt) {
		return graph.ErrInvalidGraph
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM causal_change_nodes WHERE tenant_id=$1
AND ((repository_id=$2 AND revision=$3 AND entity_id=$4) OR (repository_id=$5 AND revision=$6 AND entity_id=$7))
AND created_at BETWEEN $8 AND $9`, f.TenantID, f.From.RepositoryID, f.From.Revision, f.From.EntityID, f.To.RepositoryID, f.To.Revision, f.To.EntityID, scope.Access.NotBefore, f.CreatedAt).Scan(&count)
	if err != nil {
		return err
	}
	if count != 2 {
		return graph.ErrNotFound
	}
	raw, _ := link.MarshalJSON()
	_, err = tx.ExecContext(ctx, `INSERT INTO causal_change_links (tenant_id,link_id,from_repository,from_revision,from_entity,to_repository,to_revision,to_entity,relation,evidence_id,content_hash,canonical_link,created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT DO NOTHING`, f.TenantID, f.LinkID, f.From.RepositoryID, f.From.Revision, f.From.EntityID, f.To.RepositoryID, f.To.Revision, f.To.EntityID, string(f.Relation), f.EvidenceID, link.ContentHash(), string(raw), f.CreatedAt)
	if err != nil {
		return err
	}
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT canonical_link FROM causal_change_links WHERE tenant_id=$1 AND link_id=$2`, f.TenantID, f.LinkID).Scan(&stored)
	if err != nil {
		return err
	}
	if stored != string(raw) {
		return graph.ErrConflict
	}
	return tx.Commit()
}

const changeNeighborsSQL = `SELECT e.canonical_link,e.content_hash,e.link_id,
e.from_repository,e.from_revision,e.from_entity,e.to_repository,e.to_revision,e.to_entity,e.relation,e.evidence_id,e.created_at,
f.created_at,t.created_at,v.repository_id,v.created_at
FROM causal_change_links e
JOIN causal_change_nodes f ON (f.tenant_id=e.tenant_id AND f.repository_id=e.from_repository AND f.revision=e.from_revision AND f.entity_id=e.from_entity)
JOIN causal_change_nodes t ON (t.tenant_id=e.tenant_id AND t.repository_id=e.to_repository AND t.revision=e.to_revision AND t.entity_id=e.to_entity)
JOIN evidence_graph_nodes v ON (v.tenant_id=e.tenant_id AND v.evidence_id=e.evidence_id)
WHERE e.tenant_id=$1
AND (f.repository_id,f.revision) IN (SELECT value->>'repository_id',value->>'revision' FROM jsonb_array_elements($2::jsonb))
AND (t.repository_id,t.revision) IN (SELECT value->>'repository_id',value->>'revision' FROM jsonb_array_elements($2::jsonb))
AND v.repository_id IN (SELECT jsonb_array_elements_text($3::jsonb))
AND e.created_at BETWEEN $4 AND $5 AND f.created_at BETWEEN $4 AND e.created_at
AND t.created_at BETWEEN $4 AND e.created_at AND v.created_at BETWEEN $4 AND e.created_at
AND (($9 IN ('OUTGOING','BOTH') AND e.from_repository=$6 AND e.from_revision=$7 AND e.from_entity=$8)
OR ($9 IN ('INCOMING','BOTH') AND e.to_repository=$6 AND e.to_revision=$7 AND e.to_entity=$8))
AND ($10='' OR e.relation=$10) AND e.link_id COLLATE "C" > $11 COLLATE "C"
ORDER BY e.link_id COLLATE "C" LIMIT $12`

func (s *Postgres) ChangeNeighbors(ctx context.Context, scope graph.RevisionScope, q graph.ChangeQuery) (graph.ChangePage, error) {
	failure := func(err error) (graph.ChangePage, error) { return graph.ChangePage{Links: []graph.ChangeLink{}}, err }
	if err := s.checkRevision(ctx, scope); err != nil {
		return failure(err)
	}
	if err := q.Validate(); err != nil {
		return failure(err)
	}
	if _, err := s.GetChangeNode(ctx, scope, q.Root); err != nil {
		return failure(err)
	}
	rows, err := s.db.QueryContext(ctx, changeNeighborsSQL, scope.Access.TenantID, pins(scope), repositories(scope.Access), scope.Access.NotBefore, scope.Access.AsOf, q.Root.RepositoryID, q.Root.Revision, q.Root.EntityID, string(q.Direction), string(q.Relation), q.AfterLinkID, q.Limit+1)
	if err != nil {
		return failure(err)
	}
	defer rows.Close()
	page := graph.ChangePage{Links: []graph.ChangeLink{}}
	previous := q.AfterLinkID
	for rows.Next() {
		var raw, hash, relation, evidenceID, linkID, sourceRepo string
		var from, to graph.EntityRef
		var created, fromTime, toTime, sourceTime time.Time
		if err := rows.Scan(&raw, &hash, &linkID, &from.RepositoryID, &from.Revision, &from.EntityID, &to.RepositoryID, &to.Revision, &to.EntityID, &relation, &evidenceID, &created, &fromTime, &toTime, &sourceRepo, &sourceTime); err != nil {
			return failure(err)
		}
		link, err := graph.DecodeChangeLink([]byte(raw))
		if err != nil {
			return failure(graph.ErrGraphData)
		}
		f, _ := link.Fields()
		matches := (q.Direction == graph.Outgoing || q.Direction == graph.Both) && f.From == q.Root || (q.Direction == graph.Incoming || q.Direction == graph.Both) && f.To == q.Root
		if link.ContentHash() != hash || f.TenantID != scope.Access.TenantID || f.LinkID != linkID || f.From != from || f.To != to || string(f.Relation) != relation || f.EvidenceID != evidenceID || !f.CreatedAt.Equal(created) || !matches || q.Relation != "" && q.Relation != f.Relation || linkID <= previous || !scope.Includes(from, fromTime) || !scope.Includes(to, toTime) || !scope.Access.Contains(sourceRepo, sourceTime) || !scope.Includes(from, created) || !scope.Includes(to, created) || fromTime.After(created) || toTime.After(created) || sourceTime.After(created) || len(page.Links) > q.Limit {
			return failure(graph.ErrGraphData)
		}
		previous = linkID
		page.Links = append(page.Links, link)
	}
	if err := rows.Err(); err != nil {
		return failure(err)
	}
	if len(page.Links) > q.Limit {
		page.Links = page.Links[:q.Limit]
		last, _ := page.Links[len(page.Links)-1].Fields()
		page.NextCursor = last.LinkID
	}
	return page, nil
}
