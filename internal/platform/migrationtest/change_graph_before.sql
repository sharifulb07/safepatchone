CREATE TEMP TABLE causal_editions_before AS SELECT * FROM edition_versions;
CREATE TEMP TABLE causal_features_before AS SELECT * FROM edition_features;
CREATE TEMP TABLE causal_templates_before AS SELECT * FROM edition_limits;
INSERT INTO quota_accounts VALUES ('org');
INSERT INTO entitlement_limits VALUES ('org','retention_days','UNLIMITED',0);
INSERT INTO quota_usage VALUES ('org','repositories','lifetime',0,1);
INSERT INTO evidence_graph_nodes VALUES ('org','source','repo_a',repeat('a',64),
    json_build_object('tenant_id','org','evidence_id','source','content_hash',repeat('a',64),'created_at','2026-09-06T12:00:00Z')::text,'2026-09-06T12:00:00Z');
CREATE TEMP TABLE causal_evidence_before AS SELECT * FROM evidence_graph_nodes;
