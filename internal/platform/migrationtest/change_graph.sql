-- SQL constraint fixtures. Canonical encoding and digest validation are tested
-- through the Go model/adapter; these fixtures intentionally use dummy hashes.
CREATE FUNCTION pg_temp.change_node(ten text, repo text, rev text, entity text, typ text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO causal_change_nodes VALUES (ten,repo,rev,entity,typ,repeat('a',64),
        json_build_object('schema','safepatch.changegraph.node.v1','tenant_id',ten,
            'ref',json_build_object('repository_id',repo,'revision',rev,'entity_id',entity),
            'kind',typ,'created_at','2026-09-06T12:00:00Z')::text,'2026-09-06T12:00:00Z');
END;
$$;
CREATE FUNCTION pg_temp.change_link(ten text, id text, fr text, fv text, fe text, tr text, tv text, te text, rel text, ev text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO causal_change_links VALUES (ten,id,fr,fv,fe,tr,tv,te,rel,ev,repeat('a',64),
        json_build_object('schema','safepatch.changegraph.link.v1','tenant_id',ten,'link_id',id,
            'from',json_build_object('repository_id',fr,'revision',fv,'entity_id',fe),
            'to',json_build_object('repository_id',tr,'revision',tv,'entity_id',te),
            'relation',rel,'basis','UNKNOWN','evidence_id',ev,'created_at','2026-09-06T12:00:00Z')::text,'2026-09-06T12:00:00Z');
END;
$$;
DO $$
DECLARE kind text; rel text; statement text;
BEGIN
    IF EXISTS ((SELECT * FROM causal_editions_before EXCEPT SELECT * FROM edition_versions)
        UNION ALL (SELECT * FROM edition_versions EXCEPT SELECT * FROM causal_editions_before))
        OR EXISTS ((SELECT * FROM causal_features_before EXCEPT SELECT * FROM edition_features)
        UNION ALL (SELECT * FROM edition_features EXCEPT SELECT * FROM causal_features_before))
        OR EXISTS ((SELECT * FROM causal_templates_before EXCEPT SELECT * FROM edition_limits)
        UNION ALL (SELECT * FROM edition_limits EXCEPT SELECT * FROM causal_templates_before))
        OR EXISTS ((SELECT * FROM causal_evidence_before EXCEPT SELECT * FROM evidence_graph_nodes)
        UNION ALL (SELECT * FROM evidence_graph_nodes EXCEPT SELECT * FROM causal_evidence_before)) THEN
        RAISE EXCEPTION 'migration changed existing history';
    END IF;
    FOREACH kind IN ARRAY ARRAY['file','symbol','api','db','event','service','test','dependency','runtime','incident'] LOOP
        PERFORM pg_temp.change_node('org','repo_a','rev1',kind,kind);
        PERFORM pg_temp.change_node('org','repo_a','rev2',kind,kind);
    END LOOP;
    PERFORM pg_temp.change_node('org','repo_b','rev3','service','service');
    PERFORM pg_temp.change_node('other','repo_a','rev1','foreign','symbol');
    FOREACH rel IN ARRAY ARRAY['CONTAINS','CALLS','READS','WRITES','PUBLISHES','CONSUMES','DEPENDS_ON','TESTS','RUNTIME_CALLS','CAUSES'] LOOP
        PERFORM pg_temp.change_link('org',rel,'repo_a','rev1','symbol','repo_b','rev3','service',rel,'source');
    END LOOP;
    PERFORM pg_temp.change_link('org','next_revision','repo_a','rev2','symbol','repo_b','rev3','service','CALLS','source');
    BEGIN
        PERFORM pg_temp.change_link('org','foreign','repo_a','rev1','symbol','repo_a','rev1','foreign','CALLS','source');
        RAISE EXCEPTION 'cross-tenant endpoint accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        PERFORM pg_temp.change_link('org','missing_rev','repo_a','missing','symbol','repo_b','rev3','service','CALLS','source');
        RAISE EXCEPTION 'missing revision accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        PERFORM pg_temp.change_link('org','mixed_rev','repo_a','rev1','symbol','repo_a','rev2','file','CALLS','source');
        RAISE EXCEPTION 'same-repository mixed revision accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        PERFORM pg_temp.change_link('org','no_evidence','repo_a','rev1','symbol','repo_b','rev3','service','CALLS','missing');
        RAISE EXCEPTION 'missing evidence accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        PERFORM pg_temp.change_link('org','unknown_relation','repo_a','rev1','symbol','repo_b','rev3','service','UNKNOWN','source');
        RAISE EXCEPTION 'unknown relation accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        PERFORM pg_temp.change_node('org','repo_a','rev1','symbol','symbol');
        RAISE EXCEPTION 'duplicate revision entity accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    BEGIN
        INSERT INTO causal_change_nodes SELECT tenant_id,repository_id,revision,'altered',kind,content_hash,canonical_node,created_at
            FROM causal_change_nodes WHERE tenant_id='org' AND repository_id='repo_a' AND revision='rev1' AND entity_id='symbol';
        RAISE EXCEPTION 'canonical identity mismatch accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    FOREACH statement IN ARRAY ARRAY[
        'UPDATE causal_change_nodes SET kind=''api''', 'DELETE FROM causal_change_nodes',
        'TRUNCATE causal_change_nodes CASCADE', 'UPDATE causal_change_links SET relation=''CALLS''',
        'DELETE FROM causal_change_links', 'TRUNCATE causal_change_links'] LOOP
        BEGIN
            EXECUTE statement;
            RAISE EXCEPTION 'immutable history changed';
        EXCEPTION WHEN check_violation THEN NULL; END;
    END LOOP;
    IF (SELECT count(*) FROM causal_change_nodes WHERE tenant_id='org' AND repository_id='repo_a' AND entity_id='symbol') <> 2 THEN
        RAISE EXCEPTION 'revision history lost';
    END IF;
    IF (SELECT count(*) FROM causal_change_links e
        WHERE e.tenant_id='org' AND e.from_repository='repo_a' AND e.from_revision='rev1' AND e.from_entity='symbol'
        AND (e.to_repository,e.to_revision) IN (SELECT value->>'repository_id',value->>'revision'
            FROM jsonb_array_elements('[{"repository_id":"repo_b","revision":"rev3"}]'::jsonb))) <> 10 THEN
        RAISE EXCEPTION 'revision-pinned adjacency mixed snapshots';
    END IF;
END;
$$;
CREATE TEMP TABLE causal_nodes_before AS SELECT * FROM causal_change_nodes;
CREATE TEMP TABLE causal_links_before AS SELECT * FROM causal_change_links;
UPDATE entitlement_limits SET state='INTEGER',value=1 WHERE organization_id='org' AND limit_key='retention_days';
UPDATE entitlement_limits SET state='DISABLED',value=0 WHERE organization_id='org' AND limit_key='retention_days';
UPDATE entitlement_limits SET state='UNLIMITED',value=0 WHERE organization_id='org' AND limit_key='retention_days';
DO $$
BEGIN
    IF EXISTS ((SELECT * FROM causal_nodes_before EXCEPT SELECT * FROM causal_change_nodes)
        UNION ALL (SELECT * FROM causal_change_nodes EXCEPT SELECT * FROM causal_nodes_before))
        OR EXISTS ((SELECT * FROM causal_links_before EXCEPT SELECT * FROM causal_change_links)
        UNION ALL (SELECT * FROM causal_change_links EXCEPT SELECT * FROM causal_links_before))
        OR (SELECT committed FROM quota_usage WHERE organization_id='org') <> 1 THEN
        RAISE EXCEPTION 'plan changes corrupted causal graph/accounting history';
    END IF;
END;
$$;
