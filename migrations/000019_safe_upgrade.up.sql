BEGIN;
INSERT INTO feature_identities(feature_id,key) VALUES ('feat_0044', 'automation.safe_upgrade');
INSERT INTO quota_limit_keys(key,accounting) VALUES ('safe_upgrades_monthly', 'MONTHLY');
ALTER TABLE quota_usage DROP CONSTRAINT quota_usage_resource_period;
ALTER TABLE quota_usage ADD CONSTRAINT quota_usage_resource_period CHECK (
 (limit_key IN ('proof_credits_monthly','replay_requests_monthly','fuzz_seconds_monthly','safe_upgrades_monthly') AND period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$' AND left(period,4)<>'0000')
 OR (limit_key IN ('repositories','developers','verification_concurrency','artifact_storage_bytes','replay_concurrency') AND period='lifetime')
);
-- Preserve existing reservation identities and prohibit refunds of consumed upgrades.
CREATE OR REPLACE FUNCTION guard_quota_reservation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP <> 'UPDATE' THEN
  RAISE EXCEPTION 'reservation history cannot be removed' USING ERRCODE='23514';
 END IF;
 IF ROW(NEW.organization_id,NEW.reservation_id,NEW.limit_key,NEW.period,NEW.amount)
  IS DISTINCT FROM ROW(OLD.organization_id,OLD.reservation_id,OLD.limit_key,OLD.period,OLD.amount)
  OR (OLD.state='RELEASED' AND NEW.state<>OLD.state)
  OR (OLD.state='COMMITTED' AND NEW.state<>OLD.state AND
   (NEW.state<>'RELEASED' OR OLD.limit_key IN ('proof_credits_monthly','replay_requests_monthly','fuzz_seconds_monthly','safe_upgrades_monthly'))) THEN
  RAISE EXCEPTION 'invalid reservation transition' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;

DO $seed$
DECLARE config jsonb := $configuration$
{
  "schema": "safepatch.edition-updates.v1",
  "updates": [
    {
      "base_version": "developer-2026-v2",
      "version": {
        "edition_version_id": "developer-2026-v3",
        "edition_key": "developer",
        "created_at": "2026-09-07T00:02:00Z"
      },
      "features": [
        {
          "edition_version_id": "developer-2026-v3",
          "feature_id": "feat_0044",
          "enabled": true,
          "limit_value": null,
          "configuration_json": {
            "release": "0.2.0",
            "availability": "SDK"
          }
        }
      ],
      "limits": [
        {
          "key": "safe_upgrades_monthly",
          "state": "INTEGER",
          "value": 10
        }
      ]
    },
    {
      "base_version": "team-2026-v2",
      "version": {
        "edition_version_id": "team-2026-v3",
        "edition_key": "team",
        "created_at": "2026-09-07T00:02:00Z"
      },
      "features": [
        {
          "edition_version_id": "team-2026-v3",
          "feature_id": "feat_0044",
          "enabled": true,
          "limit_value": null,
          "configuration_json": {
            "release": "0.2.0",
            "availability": "SDK"
          }
        }
      ],
      "limits": [
        {
          "key": "safe_upgrades_monthly",
          "state": "INTEGER",
          "value": 100
        }
      ]
    },
    {
      "base_version": "business-2026-v3",
      "version": {
        "edition_version_id": "business-2026-v4",
        "edition_key": "business",
        "created_at": "2026-09-07T00:02:00Z"
      },
      "features": [
        {
          "edition_version_id": "business-2026-v4",
          "feature_id": "feat_0044",
          "enabled": true,
          "limit_value": null,
          "configuration_json": {
            "release": "0.2.0",
            "availability": "SDK"
          }
        }
      ],
      "limits": [
        {
          "key": "safe_upgrades_monthly",
          "state": "INTEGER",
          "value": 1000
        }
      ]
    },
    {
      "base_version": "enterprise-2026-v4",
      "version": {
        "edition_version_id": "enterprise-2026-v5",
        "edition_key": "enterprise",
        "created_at": "2026-09-07T00:02:00Z"
      },
      "features": [
        {
          "edition_version_id": "enterprise-2026-v5",
          "feature_id": "feat_0044",
          "enabled": true,
          "limit_value": null,
          "configuration_json": {
            "release": "0.2.0",
            "availability": "SDK"
          }
        }
      ],
      "limits": [
        {
          "key": "safe_upgrades_monthly",
          "state": "UNLIMITED",
          "value": 0
        }
      ]
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
  base:=entry->>'base_version'; target:=entry->'version'->>'edition_version_id';
  IF NOT EXISTS(SELECT 1 FROM edition_versions WHERE edition_version_id=base AND sealed AND edition_key=entry->'version'->>'edition_key') THEN
   RAISE EXCEPTION 'safe upgrade requires sealed base' USING ERRCODE='23514';
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
