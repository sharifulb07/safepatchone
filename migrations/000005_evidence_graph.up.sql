BEGIN;

-- One graph for all editions. Tenant/repository membership is checked by trusted
-- admission; these identifiers do not create organization or repository accounts.
CREATE TABLE evidence_graph_nodes (
    tenant_id text NOT NULL CHECK (tenant_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    evidence_id text NOT NULL CHECK (evidence_id ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$'),
    repository_id text NOT NULL CHECK (repository_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    canonical_evidence text NOT NULL CHECK (octet_length(canonical_evidence) BETWEEN 2 AND 1048576),
    created_at timestamptz NOT NULL CHECK (isfinite(created_at)),
    PRIMARY KEY (tenant_id,evidence_id),
    CHECK (jsonb_typeof(canonical_evidence::jsonb) = 'object'),
    CHECK ((canonical_evidence::jsonb->>'tenant_id') IS NOT DISTINCT FROM tenant_id),
    CHECK ((canonical_evidence::jsonb->>'evidence_id') IS NOT DISTINCT FROM evidence_id),
    CHECK ((canonical_evidence::jsonb->>'content_hash') IS NOT DISTINCT FROM content_hash),
    -- Canonical Evidence retains nanoseconds; truncate its UTC projection before
    -- parsing, since timestamptz would otherwise round the final fractional digit.
    CHECK ((regexp_replace(canonical_evidence::jsonb->>'created_at', '([.][0-9]{6})[0-9]+Z$', '\1Z')::timestamptz) IS NOT DISTINCT FROM created_at)
);
CREATE INDEX evidence_graph_nodes_scope ON evidence_graph_nodes (tenant_id,repository_id,created_at,evidence_id);

CREATE TABLE evidence_graph_edges (
    tenant_id text NOT NULL,
    edge_id text COLLATE "C" NOT NULL CHECK (edge_id ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$'),
    from_evidence_id text NOT NULL,
    to_evidence_id text NOT NULL,
    relationship text NOT NULL CHECK (relationship IN ('SUPPORTS','CONTRADICTS','DERIVED_FROM','VALIDATES','INVALIDATES','DEPENDS_ON')),
    created_at timestamptz NOT NULL CHECK (isfinite(created_at)),
    PRIMARY KEY (tenant_id,edge_id),
    UNIQUE (tenant_id,from_evidence_id,to_evidence_id,relationship),
    FOREIGN KEY (tenant_id,from_evidence_id) REFERENCES evidence_graph_nodes(tenant_id,evidence_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id,to_evidence_id) REFERENCES evidence_graph_nodes(tenant_id,evidence_id) ON DELETE RESTRICT,
    CHECK (from_evidence_id <> to_evidence_id)
);
CREATE INDEX evidence_graph_edges_outgoing ON evidence_graph_edges (tenant_id,from_evidence_id,edge_id);
CREATE INDEX evidence_graph_edges_incoming ON evidence_graph_edges (tenant_id,to_evidence_id,edge_id);

CREATE FUNCTION reject_evidence_graph_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'evidence graph history is immutable' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER evidence_graph_nodes_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON evidence_graph_nodes
    FOR EACH STATEMENT EXECUTE FUNCTION reject_evidence_graph_change();
CREATE TRIGGER evidence_graph_edges_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON evidence_graph_edges
    FOR EACH STATEMENT EXECUTE FUNCTION reject_evidence_graph_change();

COMMIT;
