BEGIN;
-- Add a stable organization-scoped global change capability.
INSERT INTO feature_identities(feature_id,key) VALUES
('feat_0043', 'graph.global_change');

-- Exact additive configuration mirrored by internal/config/global_patch_bundles.json.
DO $seed$
DECLARE config jsonb := $configuration$
{
  "schema": "safepatch.edition-updates.v1",
  "updates": [
    {
      "base_version": "enterprise-2026-v3",
      "version": {"edition_version_id": "enterprise-2026-v4", "edition_key": "enterprise", "created_at": "2026-09-07T00:01:00Z"},
      "features": [{"edition_version_id": "enterprise-2026-v4", "feature_id": "feat_0043", "enabled": true, "limit_value": null, "configuration_json": {"release": "0.2.0", "availability": "SDK"}}]
    }
  ]
}
$configuration$::jsonb;
entry jsonb;
mapping jsonb;
base text;
target text;
BEGIN
 FOR entry IN SELECT value FROM jsonb_array_elements(config->'updates') LOOP
  base := entry->>'base_version';
  target := entry->'version'->>'edition_version_id';
  IF NOT EXISTS (SELECT 1 FROM edition_versions WHERE edition_version_id=base AND sealed AND edition_key=entry->'version'->>'edition_key') THEN
   RAISE EXCEPTION 'global patch bundle requires sealed base' USING ERRCODE='23514';
  END IF;
  INSERT INTO edition_versions(edition_version_id,edition_key,created_at)
   VALUES(target,entry->'version'->>'edition_key',(entry->'version'->>'created_at')::timestamptz);
  INSERT INTO edition_features(edition_version_id,feature_id,enabled,limit_value,configuration_json)
   SELECT target,feature_id,enabled,limit_value,configuration_json FROM edition_features f
    WHERE edition_version_id=base AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(entry->'features') replacement WHERE replacement->>'feature_id'=f.feature_id);
  FOR mapping IN SELECT value FROM jsonb_array_elements(entry->'features') LOOP
   INSERT INTO edition_features(edition_version_id,feature_id,enabled,limit_value,configuration_json)
    VALUES(target,mapping->>'feature_id',(mapping->>'enabled')::boolean,(mapping->>'limit_value')::bigint,mapping->'configuration_json');
  END LOOP;
  INSERT INTO edition_limits(edition_version_id,limit_key,state,value)
   SELECT target,limit_key,state,value FROM edition_limits WHERE edition_version_id=base;
  UPDATE edition_versions SET sealed=true WHERE edition_version_id=target;
 END LOOP;
END;
$seed$;
COMMIT;

