CREATE TEMP TABLE v02_versions_before AS SELECT * FROM edition_versions;
CREATE TEMP TABLE v02_features_before AS SELECT * FROM edition_features;
INSERT INTO quota_accounts VALUES ('org_pinned');
INSERT INTO entitlement_limits VALUES ('org_pinned','repositories','INTEGER',3);
INSERT INTO quota_usage VALUES ('org_pinned','repositories','lifetime',0,3);
INSERT INTO quota_reservations VALUES ('org_pinned','existing','repositories','lifetime',3,'COMMITTED');
-- Constraint fixtures: the Go adapter separately verifies complete sealed Evidence.
INSERT INTO evidence_graph_nodes VALUES ('org_pinned','old_a','repo',repeat('a',64),
    json_build_object('tenant_id','org_pinned','evidence_id','old_a','content_hash',repeat('a',64),'created_at','2026-09-06T00:00:00Z')::text,'2026-09-06T00:00:00Z');
INSERT INTO evidence_graph_nodes VALUES ('org_pinned','old_b','repo',repeat('a',64),
    json_build_object('tenant_id','org_pinned','evidence_id','old_b','content_hash',repeat('a',64),'created_at','2026-09-06T00:00:00Z')::text,'2026-09-06T00:00:00Z');
INSERT INTO evidence_graph_edges VALUES ('org_pinned','existing_edge','old_a','old_b','SUPPORTS','2026-09-06T00:00:00Z');
CREATE TEMP TABLE v02_nodes_before AS SELECT * FROM evidence_graph_nodes;
CREATE TEMP TABLE v02_edges_before AS SELECT * FROM evidence_graph_edges;
