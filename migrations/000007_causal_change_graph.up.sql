BEGIN;

CREATE TABLE causal_change_nodes (
    tenant_id text NOT NULL CHECK (tenant_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    repository_id text NOT NULL CHECK (repository_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    revision text NOT NULL CHECK (revision ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$'),
    entity_id text COLLATE "C" NOT NULL CHECK (entity_id ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$'),
    kind text NOT NULL CHECK (kind IN ('file','symbol','api','db','event','service','test','dependency','runtime','incident')),
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    canonical_node text NOT NULL CHECK (octet_length(canonical_node) BETWEEN 2 AND 131072),
    created_at timestamptz NOT NULL CHECK (isfinite(created_at)),
    PRIMARY KEY (tenant_id,repository_id,revision,entity_id),
    CHECK (jsonb_typeof(canonical_node::jsonb) = 'object'),
    CHECK ((canonical_node::jsonb->>'schema') IS NOT DISTINCT FROM 'safepatch.changegraph.node.v1'),
    CHECK ((canonical_node::jsonb->>'tenant_id') IS NOT DISTINCT FROM tenant_id),
    CHECK ((canonical_node::jsonb->'ref'->>'repository_id') IS NOT DISTINCT FROM repository_id),
    CHECK ((canonical_node::jsonb->'ref'->>'revision') IS NOT DISTINCT FROM revision),
    CHECK ((canonical_node::jsonb->'ref'->>'entity_id') IS NOT DISTINCT FROM entity_id),
    CHECK ((canonical_node::jsonb->>'kind') IS NOT DISTINCT FROM kind),
    CHECK ((canonical_node::jsonb->>'created_at')::timestamptz IS NOT DISTINCT FROM created_at)
);
CREATE INDEX causal_change_nodes_scope ON causal_change_nodes (tenant_id,repository_id,revision,created_at,entity_id);

CREATE TABLE causal_change_links (
    tenant_id text NOT NULL,
    link_id text COLLATE "C" NOT NULL CHECK (link_id ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$'),
    from_repository text NOT NULL,
    from_revision text NOT NULL,
    from_entity text NOT NULL,
    to_repository text NOT NULL,
    to_revision text NOT NULL,
    to_entity text NOT NULL,
    relation text NOT NULL CHECK (relation IN ('CONTAINS','CALLS','READS','WRITES','PUBLISHES','CONSUMES','DEPENDS_ON','TESTS','RUNTIME_CALLS','CAUSES')),
    evidence_id text NOT NULL,
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    canonical_link text NOT NULL CHECK (octet_length(canonical_link) BETWEEN 2 AND 131072),
    created_at timestamptz NOT NULL CHECK (isfinite(created_at)),
    PRIMARY KEY (tenant_id,link_id),
    FOREIGN KEY (tenant_id,from_repository,from_revision,from_entity) REFERENCES causal_change_nodes(tenant_id,repository_id,revision,entity_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id,to_repository,to_revision,to_entity) REFERENCES causal_change_nodes(tenant_id,repository_id,revision,entity_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id,evidence_id) REFERENCES evidence_graph_nodes(tenant_id,evidence_id) ON DELETE RESTRICT,
    CHECK ((from_repository,from_revision,from_entity) <> (to_repository,to_revision,to_entity)),
    CHECK (from_repository <> to_repository OR from_revision = to_revision),
    CHECK (jsonb_typeof(canonical_link::jsonb) = 'object'),
    CHECK ((canonical_link::jsonb->>'schema') IS NOT DISTINCT FROM 'safepatch.changegraph.link.v1'),
    CHECK ((canonical_link::jsonb->>'tenant_id') IS NOT DISTINCT FROM tenant_id),
    CHECK ((canonical_link::jsonb->>'link_id') IS NOT DISTINCT FROM link_id),
    CHECK ((canonical_link::jsonb->'from'->>'repository_id') IS NOT DISTINCT FROM from_repository),
    CHECK ((canonical_link::jsonb->'from'->>'revision') IS NOT DISTINCT FROM from_revision),
    CHECK ((canonical_link::jsonb->'from'->>'entity_id') IS NOT DISTINCT FROM from_entity),
    CHECK ((canonical_link::jsonb->'to'->>'repository_id') IS NOT DISTINCT FROM to_repository),
    CHECK ((canonical_link::jsonb->'to'->>'revision') IS NOT DISTINCT FROM to_revision),
    CHECK ((canonical_link::jsonb->'to'->>'entity_id') IS NOT DISTINCT FROM to_entity),
    CHECK ((canonical_link::jsonb->>'relation') IS NOT DISTINCT FROM relation),
    CHECK ((canonical_link::jsonb->>'evidence_id') IS NOT DISTINCT FROM evidence_id),
    CHECK (COALESCE(canonical_link::jsonb->>'basis' IN ('OBSERVED','INFERRED','UNKNOWN'), false)),
    CHECK ((canonical_link::jsonb->>'created_at')::timestamptz IS NOT DISTINCT FROM created_at)
);
CREATE INDEX causal_change_links_outgoing ON causal_change_links (tenant_id,from_repository,from_revision,from_entity,link_id);
CREATE INDEX causal_change_links_incoming ON causal_change_links (tenant_id,to_repository,to_revision,to_entity,link_id);

CREATE TRIGGER causal_change_nodes_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON causal_change_nodes
    FOR EACH STATEMENT EXECUTE FUNCTION reject_evidence_graph_change();
CREATE TRIGGER causal_change_links_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON causal_change_links
    FOR EACH STATEMENT EXECUTE FUNCTION reject_evidence_graph_change();

COMMIT;
