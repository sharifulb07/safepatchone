DO $$
BEGIN
    IF (SELECT count(*) FROM editions) <> 5 OR (SELECT count(*) FROM feature_identities) <> 41
        OR (SELECT count(*) FROM edition_limits) <> 40 THEN
        RAISE EXCEPTION 'incorrect v0.2 configuration counts';
    END IF;
    IF EXISTS (SELECT * FROM v02_versions_before EXCEPT SELECT * FROM edition_versions)
        OR EXISTS (SELECT * FROM v02_features_before EXCEPT SELECT * FROM edition_features) THEN
        RAISE EXCEPTION 'v0.2 changed historical bundles';
    END IF;
    IF EXISTS ((SELECT * FROM v02_nodes_before EXCEPT SELECT * FROM evidence_graph_nodes)
        UNION ALL (SELECT * FROM evidence_graph_nodes EXCEPT SELECT * FROM v02_nodes_before))
        OR EXISTS ((SELECT * FROM v02_edges_before EXCEPT SELECT * FROM evidence_graph_edges)
        UNION ALL (SELECT * FROM evidence_graph_edges EXCEPT SELECT * FROM v02_edges_before)) THEN
        RAISE EXCEPTION 'v0.2 changed evidence history';
    END IF;
    IF (SELECT count(*) FROM entitlement_limits) <> 1
        OR (SELECT value FROM entitlement_limits WHERE organization_id='org_pinned') <> 3
        OR (SELECT committed FROM quota_usage WHERE organization_id='org_pinned') <> 3
        OR (SELECT state FROM quota_reservations WHERE organization_id='org_pinned') <> 'COMMITTED' THEN
        RAISE EXCEPTION 'matrix implicitly reassigned effective customer policy';
    END IF;
    IF EXISTS (SELECT 1 FROM edition_limits l JOIN v02_versions_before v USING (edition_version_id)) THEN
        RAISE EXCEPTION 'historical versions received invented limits';
    END IF;
    IF (SELECT count(*) FROM edition_features WHERE feature_id='feat_0040' AND enabled) <> 5
        OR (SELECT count(*) FROM edition_features WHERE feature_id='feat_0041' AND enabled) <> 4 THEN
        RAISE EXCEPTION 'local policy allocation incorrect';
    END IF;
    IF EXISTS (SELECT 1 FROM edition_limits b JOIN edition_limits t USING (limit_key)
        WHERE b.edition_version_id='business-2026-v2' AND t.edition_version_id='team-2026-v2'
        AND t.state='INTEGER' AND (b.state<>'INTEGER' OR b.value<=t.value)) THEN
        RAISE EXCEPTION 'Business limits are not higher';
    END IF;
    IF EXISTS (SELECT 1 FROM edition_limits WHERE limit_key='replay_requests_monthly' AND state<>'DISABLED') THEN
        RAISE EXCEPTION 'future replay received execution capacity';
    END IF;
    IF (SELECT count(*) FROM edition_features WHERE edition_version_id='enterprise-2026-v2'
        AND feature_id IN ('feat_0026','feat_0027') AND configuration_json->>'scope'='foundations'
        AND configuration_json->>'availability'='FUTURE') <> 2 THEN
        RAISE EXCEPTION 'Enterprise foundations imply full availability';
    END IF;
    BEGIN
        UPDATE edition_limits SET value=999 WHERE edition_version_id='team-2026-v2';
        RAISE EXCEPTION 'published limit replaced';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        DELETE FROM edition_limits WHERE edition_version_id='community-2026-v3';
        RAISE EXCEPTION 'published limit removed';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        TRUNCATE edition_limits;
        RAISE EXCEPTION 'limit history truncated';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO edition_limits VALUES ('community-2026-v2','repositories','UNLIMITED',0);
        RAISE EXCEPTION 'sealed historical version extended';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO edition_limits VALUES ('community-2099-v99','repositories','UNLIMITED',0);
        RAISE EXCEPTION 'orphan limit accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    INSERT INTO edition_versions (edition_version_id,edition_key,created_at) VALUES ('team-2099-v99','team','2099-01-01T00:00:00Z');
    BEGIN
        INSERT INTO edition_limits VALUES ('team-2099-v99','repositories','UNKNOWN',0);
        RAISE EXCEPTION 'unknown state accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO edition_limits VALUES ('team-2099-v99','repositories','INTEGER',-1);
        RAISE EXCEPTION 'negative quota accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO edition_limits VALUES ('team-2099-v99','repositories','UNLIMITED',1);
        RAISE EXCEPTION 'unlimited magic number accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO edition_limits VALUES ('team-2099-v99','unregistered','INTEGER',1);
        RAISE EXCEPTION 'unknown limit key accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    INSERT INTO edition_limits VALUES ('team-2099-v99','repositories','UNLIMITED',0);
    BEGIN
        INSERT INTO edition_limits VALUES ('team-2099-v99','repositories','DISABLED',0);
        RAISE EXCEPTION 'duplicate quota accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    UPDATE edition_versions SET sealed=true WHERE edition_version_id='team-2099-v99';
END;
$$;
