INSERT INTO quota_accounts VALUES ('legacy');
INSERT INTO entitlement_limits VALUES ('legacy','proof_credits_monthly','INTEGER',100);
INSERT INTO quota_usage VALUES ('legacy','proof_credits_monthly','2026-09',1,2);
INSERT INTO quota_reservations VALUES ('legacy','hold','proof_credits_monthly','2026-09',1,'RESERVED');
INSERT INTO evidence_graph_nodes VALUES ('evidence_only','evidence','repo',repeat('a',64),
    json_build_object('tenant_id','evidence_only','evidence_id','evidence','content_hash',repeat('a',64),'created_at','2026-09-06T12:00:00Z')::text,'2026-09-06T12:00:00Z');
CREATE TEMP TABLE saas_usage_before AS SELECT * FROM quota_usage;
CREATE TEMP TABLE saas_holds_before AS SELECT * FROM quota_reservations;
CREATE TEMP TABLE saas_evidence_before AS SELECT * FROM evidence_graph_nodes;
CREATE TEMP TABLE saas_editions_before AS SELECT * FROM edition_versions;
CREATE TEMP TABLE saas_grants_before AS SELECT * FROM edition_features;
