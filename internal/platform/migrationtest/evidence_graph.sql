-- These are SQL constraint fixtures, not sealed Evidence. Full canonical schema
-- and SHA-256 validation belong to the Go Evidence boundary and adapter tests.
DO $$
DECLARE tenant text; node text; relation text; stamp timestamptz := '2026-09-06T12:00:00.123456Z';
BEGIN
    IF EXISTS ((SELECT * FROM graph_editions_before EXCEPT SELECT * FROM edition_versions)
        UNION ALL (SELECT * FROM edition_versions EXCEPT SELECT * FROM graph_editions_before))
        OR EXISTS ((SELECT * FROM graph_grants_before EXCEPT SELECT * FROM edition_features)
        UNION ALL (SELECT * FROM edition_features EXCEPT SELECT * FROM graph_grants_before)) THEN
        RAISE EXCEPTION 'graph migration changed commercial history';
    END IF;
    IF (SELECT committed FROM quota_usage WHERE organization_id='org') <> 1
        OR (SELECT state FROM quota_reservations WHERE organization_id='org') <> 'COMMITTED' THEN
        RAISE EXCEPTION 'graph migration changed existing accounting';
    END IF;
    FOREACH tenant IN ARRAY ARRAY['org','other'] LOOP
        FOREACH node IN ARRAY ARRAY['a','b'] LOOP
            INSERT INTO evidence_graph_nodes VALUES (tenant,node,'repo_'||node,repeat('a',64),
                json_build_object('tenant_id',tenant,'evidence_id',node,'content_hash',repeat('a',64),
                    'created_at','2026-09-06T12:00:00.123456999Z')::text,stamp);
        END LOOP;
    END LOOP;
    -- Same IDs in another tenant are independent, never global identities.
    FOREACH relation IN ARRAY ARRAY['SUPPORTS','CONTRADICTS','DERIVED_FROM','VALIDATES','INVALIDATES','DEPENDS_ON'] LOOP
        INSERT INTO evidence_graph_edges VALUES ('org',relation,'a','b',relation,stamp);
        INSERT INTO evidence_graph_edges VALUES ('other',relation,'a','b',relation,stamp);
    END LOOP;
    INSERT INTO evidence_graph_edges VALUES ('org','cycle','b','a','DEPENDS_ON',stamp);
    BEGIN
        INSERT INTO evidence_graph_edges VALUES ('org','self','a','a','SUPPORTS',stamp);
        RAISE EXCEPTION 'self relationship accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO evidence_graph_edges VALUES ('org','unknown','a','b','UNKNOWN',stamp);
        RAISE EXCEPTION 'unknown relationship accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO evidence_graph_edges VALUES ('org','duplicate','a','b','SUPPORTS',stamp);
        RAISE EXCEPTION 'duplicate semantic relationship accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    BEGIN
        INSERT INTO evidence_graph_edges VALUES ('unowned','cross_tenant','a','b','SUPPORTS',stamp);
        RAISE EXCEPTION 'foreign tenant endpoints accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    INSERT INTO evidence_graph_nodes VALUES ('other','foreign','repo_secret',repeat('a',64),
        json_build_object('tenant_id','other','evidence_id','foreign','content_hash',repeat('a',64),'created_at',stamp)::text,stamp);
    BEGIN
        INSERT INTO evidence_graph_edges VALUES ('org','cross_tenant','a','foreign','SUPPORTS',stamp);
        RAISE EXCEPTION 'foreign endpoint accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        INSERT INTO evidence_graph_nodes VALUES ('org','mismatch','repo',repeat('a',64),
            json_build_object('tenant_id','other','evidence_id','mismatch','content_hash',repeat('a',64),'created_at',stamp)::text,stamp);
        RAISE EXCEPTION 'mismatched evidence tenant accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO evidence_graph_nodes SELECT * FROM evidence_graph_nodes WHERE tenant_id='org' AND evidence_id='a';
        RAISE EXCEPTION 'duplicate node accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    BEGIN
        UPDATE evidence_graph_nodes SET repository_id='other_repo' WHERE tenant_id='org';
        RAISE EXCEPTION 'node moved';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        UPDATE evidence_graph_edges SET relationship='INVALIDATES' WHERE edge_id='cycle';
        RAISE EXCEPTION 'edge changed';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        DELETE FROM evidence_graph_edges WHERE tenant_id='org';
        RAISE EXCEPTION 'edge erased';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        DELETE FROM evidence_graph_nodes WHERE tenant_id='other' AND evidence_id='foreign';
        RAISE EXCEPTION 'node erased';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        TRUNCATE evidence_graph_edges;
        RAISE EXCEPTION 'graph history truncated';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        TRUNCATE evidence_graph_nodes CASCADE;
        RAISE EXCEPTION 'evidence history truncated';
    EXCEPTION WHEN check_violation THEN NULL; END;
END;
$$;

CREATE TEMP TABLE graph_nodes_before AS SELECT * FROM evidence_graph_nodes;
CREATE TEMP TABLE graph_edges_before AS SELECT * FROM evidence_graph_edges;
UPDATE entitlement_limits SET state='INTEGER',value=1 WHERE organization_id='org' AND limit_key='retention_days';
UPDATE entitlement_limits SET state='DISABLED',value=0 WHERE organization_id='org' AND limit_key='retention_days';
UPDATE entitlement_limits SET state='UNLIMITED',value=0 WHERE organization_id='org' AND limit_key='retention_days';
DO $$
BEGIN
    IF EXISTS ((SELECT * FROM graph_nodes_before EXCEPT SELECT * FROM evidence_graph_nodes)
        UNION ALL (SELECT * FROM evidence_graph_nodes EXCEPT SELECT * FROM graph_nodes_before))
        OR EXISTS ((SELECT * FROM graph_edges_before EXCEPT SELECT * FROM evidence_graph_edges)
        UNION ALL (SELECT * FROM evidence_graph_edges EXCEPT SELECT * FROM graph_edges_before)) THEN
        RAISE EXCEPTION 'plan changes corrupted immutable graph history';
    END IF;
    IF (SELECT count(*) FROM evidence_graph_edges WHERE tenant_id='org') <> 7
        OR (SELECT count(*) FROM evidence_graph_edges WHERE tenant_id='other') <> 6 THEN
        RAISE EXCEPTION 'graph history changed after rejected writes';
    END IF;
END;
$$;
