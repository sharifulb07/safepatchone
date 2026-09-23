DO $$
BEGIN
    IF (SELECT count(*) FROM feature_identities) <> 29
        OR (SELECT count(*) FROM edition_versions) <> 1
        OR (SELECT count(*) FROM edition_features) <> 1
        OR NOT EXISTS (SELECT 1 FROM edition_versions WHERE edition_version_id='team-2026-v1' AND edition_key='team' AND sealed AND created_at='2026-01-01T00:00:00Z')
        OR NOT EXISTS (SELECT 1 FROM edition_features WHERE edition_version_id='team-2026-v1' AND feature_id='feat_0001' AND enabled AND limit_value=77 AND configuration_json='{"legacy":true}'::jsonb) THEN
        RAISE EXCEPTION 'conflicting seed did not roll back completely or changed historical data';
    END IF;
END;
$$;
