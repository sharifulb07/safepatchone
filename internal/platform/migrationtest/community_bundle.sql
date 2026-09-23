DO $$
BEGIN
    IF (SELECT count(*) FROM editions) <> 5 OR (SELECT count(*) FROM edition_versions) <> 6 THEN
        RAISE EXCEPTION 'editions or version history changed unexpectedly';
    END IF;
    IF (SELECT count(*) FROM feature_identities) <> 39
        OR NOT EXISTS (SELECT 1 FROM feature_identities WHERE feature_id = 'feat_0039' AND key = 'evidence.local') THEN
        RAISE EXCEPTION 'local evidence identity missing';
    END IF;
    IF EXISTS (SELECT * FROM community_history_before EXCEPT SELECT * FROM edition_features)
        OR EXISTS (SELECT * FROM edition_features WHERE edition_version_id <> 'community-2026-v2' EXCEPT SELECT * FROM community_history_before) THEN
        RAISE EXCEPTION 'historical edition mappings were modified';
    END IF;
    IF (SELECT count(*) FROM edition_features WHERE edition_version_id = 'community-2026-v2') <> 5
        OR EXISTS (SELECT 1 FROM edition_features WHERE edition_version_id = 'community-2026-v2'
            AND (NOT enabled OR limit_value IS NOT NULL OR configuration_json <> '{}'::jsonb
                OR feature_id NOT IN ('feat_0001', 'feat_0002', 'feat_0005', 'feat_0008', 'feat_0039'))) THEN
        RAISE EXCEPTION 'unexpected local capability grant or numeric limit';
    END IF;
    IF NOT (SELECT sealed FROM edition_versions WHERE edition_version_id = 'community-2026-v2') THEN
        RAISE EXCEPTION 'built-in bundle not sealed';
    END IF;
    IF (SELECT committed FROM quota_usage WHERE organization_id = 'org_pinned') <> 1
        OR (SELECT state FROM quota_reservations WHERE organization_id = 'org_pinned') <> 'COMMITTED'
        OR (SELECT value FROM entitlement_limits WHERE organization_id = 'org_pinned') <> 2 THEN
        RAISE EXCEPTION 'bundle publication corrupted customer quota data';
    END IF;
    BEGIN
        UPDATE edition_features SET enabled = false WHERE edition_version_id = 'community-2026-v2';
        RAISE EXCEPTION 'sealed local bundle was mutable';
    EXCEPTION WHEN check_violation THEN NULL; END;
END;
$$;
