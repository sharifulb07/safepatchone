-- The migration must not overwrite a version whose ID already exists.
SELECT publish_edition_version('team-2026-v1', 'team', '2026-01-01T00:00:00Z',
    '[{"edition_version_id":"team-2026-v1","feature_id":"feat_0001","enabled":true,"limit_value":77,"configuration_json":{"legacy":true}}]');
