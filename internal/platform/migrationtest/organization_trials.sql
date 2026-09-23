INSERT INTO organization_trials(organization_id,trial_id,edition_key,edition_version_id,starts_at,expires_at)
VALUES('trial_a','trial_'||repeat('a',64),'team','team-2026-v2','2026-09-07T12:00:00Z','2026-09-21T12:00:00Z'),
 ('trial_c','trial_'||repeat('c',64),'business','business-2026-v2','2026-09-07T12:00:00Z','2026-10-07T12:00:00Z');
INSERT INTO entitlements(organization_id,entitlement_id,feature_id,source,source_reference,canonical_record)
SELECT t.organization_id,'trial_grant',f.feature_id,'TRIAL',t.trial_id,jsonb_build_object(
 'organization_id',t.organization_id,'entitlement_id','trial_grant','feature_id',f.feature_id,'source','TRIAL','source_reference',t.trial_id,
 'enabled',f.enabled,'limit_value',f.limit_value,'starts_at',t.starts_at,'expires_at',t.expires_at,'created_at',t.starts_at,'metadata','{}'::jsonb)::text
FROM organization_trials t JOIN edition_features f ON f.edition_version_id=t.edition_version_id
WHERE t.organization_id='trial_a' AND f.feature_id='feat_0011';
INSERT INTO organization_trial_grants VALUES ('trial_a','trial_'||repeat('a',64),'trial_grant');
DO $$
DECLARE statement text;
BEGIN
 IF (SELECT count(*) FROM organization_trial_grants)<>1 THEN RAISE EXCEPTION 'trial grant missing'; END IF;
 BEGIN
  INSERT INTO organization_trials SELECT * FROM organization_trials WHERE organization_id='trial_a';
  RAISE EXCEPTION 'repeat trial accepted';
 EXCEPTION WHEN unique_violation THEN NULL; END;
 BEGIN
  INSERT INTO organization_trials(organization_id,trial_id,edition_key,edition_version_id,starts_at,expires_at)
  VALUES('trial_b','trial_'||repeat('b',64),'developer','developer-2026-v2','2026-09-07T12:00:00Z','2026-09-22T12:00:00Z');
  RAISE EXCEPTION 'extended developer trial';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  INSERT INTO organization_trial_grants VALUES ('trial_c','trial_'||repeat('a',64),'trial_grant');
  RAISE EXCEPTION 'cross tenant trial grant accepted';
 EXCEPTION WHEN check_violation OR foreign_key_violation THEN NULL; END;
 BEGIN
  INSERT INTO organization_trial_grants VALUES ('trial_a','trial_'||repeat('a',64),'legacy_trial');
  RAISE EXCEPTION 'foreign entitlement accepted';
 EXCEPTION WHEN check_violation OR foreign_key_violation THEN NULL; END;
 FOREACH statement IN ARRAY ARRAY[
  'UPDATE organization_trials SET expires_at=expires_at+interval ''1 day''',
  'DELETE FROM organization_trials','TRUNCATE organization_trials CASCADE',
  'UPDATE organization_trial_grants SET trial_id=''changed''',
  'DELETE FROM organization_trial_grants','TRUNCATE organization_trial_grants'
 ] LOOP
  BEGIN EXECUTE statement; RAISE EXCEPTION 'mutable trial history';
  EXCEPTION WHEN check_violation THEN NULL; END;
 END LOOP;
 -- Simulated transaction failure must remove both trial and entitlement writes.
 BEGIN
  INSERT INTO organization_trials(organization_id,trial_id,edition_key,edition_version_id,starts_at,expires_at)
  VALUES('trial_b','trial_'||repeat('b',64),'developer','developer-2026-v2','2026-09-07T12:00:00Z','2026-09-21T12:00:00Z');
  INSERT INTO organization_trial_grants VALUES('trial_b','trial_'||repeat('b',64),'legacy_trial');
 EXCEPTION WHEN check_violation THEN NULL; END;
 IF EXISTS(SELECT 1 FROM organization_trials WHERE organization_id='trial_b') THEN RAISE EXCEPTION 'partial trial persisted'; END IF;
END; $$;
CREATE TEMP TABLE trial_data_after AS SELECT 'repositories' AS kind,to_jsonb(r) AS value FROM repositories r
 UNION ALL SELECT 'subscriptions',to_jsonb(s) FROM subscriptions s
 UNION ALL SELECT 'members',to_jsonb(m) FROM organization_members m
 UNION ALL SELECT 'usage',to_jsonb(u) FROM quota_usage u
 UNION ALL SELECT 'reservations',to_jsonb(r) FROM quota_reservations r
 UNION ALL SELECT 'entitlements',to_jsonb(e) FROM entitlements e WHERE entitlement_id<>'trial_grant'
 UNION ALL SELECT 'bundles',to_jsonb(f) FROM edition_features f;
DO $$ BEGIN
 IF EXISTS((SELECT * FROM trial_data_before EXCEPT SELECT * FROM trial_data_after)
 UNION ALL (SELECT * FROM trial_data_after EXCEPT SELECT * FROM trial_data_before)) THEN RAISE EXCEPTION 'trial changed existing data'; END IF;
END; $$;
