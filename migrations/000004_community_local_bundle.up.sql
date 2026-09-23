BEGIN;

INSERT INTO feature_identities (feature_id, key) VALUES
('feat_0039', 'evidence.local');

-- Additive local bundle: preserve all four Community v1 grants and add local
-- evidence. Existing assignments and all sealed bundles remain untouched.
-- This JSON is also checked against the compiled bundle by the Go tests.
DO $seed$
DECLARE bundle jsonb := $bundle$
{
  "version": {
    "edition_version_id": "community-2026-v2",
    "edition_key": "community",
    "created_at": "2026-09-06T00:00:00Z"
  },
  "features": [
    {"edition_version_id":"community-2026-v2","feature_id":"feat_0001","enabled":true,"limit_value":null,"configuration_json":{}},
    {"edition_version_id":"community-2026-v2","feature_id":"feat_0002","enabled":true,"limit_value":null,"configuration_json":{}},
    {"edition_version_id":"community-2026-v2","feature_id":"feat_0005","enabled":true,"limit_value":null,"configuration_json":{}},
    {"edition_version_id":"community-2026-v2","feature_id":"feat_0008","enabled":true,"limit_value":null,"configuration_json":{}},
    {"edition_version_id":"community-2026-v2","feature_id":"feat_0039","enabled":true,"limit_value":null,"configuration_json":{}}
  ]
}
$bundle$::jsonb;
BEGIN
    PERFORM publish_edition_version(bundle->'version'->>'edition_version_id',
        bundle->'version'->>'edition_key', (bundle->'version'->>'created_at')::timestamptz,
        bundle->'features');
END;
$seed$;

COMMIT;
