-- Upgrade fixture: a customer has pinned a pre-existing, differently configured
-- version before the initial public bundles are installed.
SELECT publish_edition_version('team-2025-v1', 'team', '2025-01-01T00:00:00Z',
    '[{"edition_version_id":"team-2025-v1","feature_id":"feat_0001","enabled":true,"limit_value":7,"configuration_json":{"policy":"grandfathered"}}]');
CREATE TEMP TABLE pre_seed_versions AS SELECT to_jsonb(v) AS value FROM edition_versions v;
CREATE TEMP TABLE pre_seed_mappings AS SELECT to_jsonb(f) AS value FROM edition_features f;
