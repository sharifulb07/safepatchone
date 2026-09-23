BEGIN;
INSERT INTO feature_identities(feature_id,key) VALUES ('feat_0045', 'agent.earned_autonomy');
-- Existing capability bytes remain immutable; ordinary leases have no binding.
ALTER TABLE agent_safe_leases ADD CONSTRAINT agent_earned_binding CHECK (
 NOT (capabilities::jsonb ? 'earned') OR COALESCE(
  jsonb_typeof(capabilities::jsonb->'earned')='object'
  AND jsonb_typeof(capabilities::jsonb->'earned'->'policy_id')='string'
  AND jsonb_typeof(capabilities::jsonb->'earned'->'challenge_digest')='string'
  AND jsonb_typeof(capabilities::jsonb->'earned'->'proof_set_digest')='string'
  AND (capabilities::jsonb->'earned'->>'policy_id') ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$'
  AND (capabilities::jsonb->'earned'->>'challenge_digest') ~ '^[a-f0-9]{64}$'
  AND (capabilities::jsonb->'earned'->>'proof_set_digest') ~ '^[a-f0-9]{64}$', false)
);
DO $seed$
DECLARE config jsonb := $configuration$
{
  "schema": "safepatch.edition-updates.v1",
  "updates": [{
    "base_version": "enterprise-2026-v5",
    "version": {"edition_version_id": "enterprise-2026-v6", "edition_key": "enterprise", "created_at": "2026-09-07T00:03:00Z"},
    "features": [
      {"edition_version_id": "enterprise-2026-v6", "feature_id": "feat_0045", "enabled": true, "limit_value": 32, "configuration_json": {"availability": "SDK", "release": "0.2.0"}},
      {"edition_version_id": "enterprise-2026-v6", "feature_id": "feat_0021", "enabled": true, "limit_value": null, "configuration_json": {"availability": "SDK", "release": "0.2.0"}}
    ],
    "limits": []
  }]
}
$configuration$::jsonb;
entry jsonb;
mapping jsonb;
base text;
target text;
BEGIN
 FOR entry IN SELECT value FROM jsonb_array_elements(config->'updates') LOOP
  base:=entry->>'base_version'; target:=entry->'version'->>'edition_version_id';
  IF NOT EXISTS(SELECT 1 FROM edition_versions WHERE edition_version_id=base AND sealed AND edition_key=entry->'version'->>'edition_key') THEN
   RAISE EXCEPTION 'earned autonomy requires sealed base' USING ERRCODE='23514';
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
   SELECT target,limit_key,state,value FROM edition_limits l WHERE edition_version_id=base
    AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(entry->'limits') replacement WHERE replacement->>'key'=l.limit_key);
  FOR mapping IN SELECT value FROM jsonb_array_elements(entry->'limits') LOOP
   INSERT INTO edition_limits(edition_version_id,limit_key,state,value) VALUES(target,mapping->>'key',mapping->>'state',(mapping->>'value')::bigint);
  END LOOP;
  UPDATE edition_versions SET sealed=true WHERE edition_version_id=target;
 END LOOP;
END;
$seed$;
COMMIT;
