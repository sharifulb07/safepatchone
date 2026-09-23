DO $$
BEGIN
 IF (SELECT count(*) FROM editions)<>5 OR (SELECT count(*) FROM feature_identities)<>42
  OR NOT EXISTS(SELECT 1 FROM feature_identities WHERE feature_id='feat_0042' AND key='proof.composition') THEN
  RAISE EXCEPTION 'composition identities changed';
 END IF;
 IF (SELECT count(*) FROM edition_features WHERE feature_id='feat_0042' AND enabled
  AND edition_version_id IN ('business-2026-v3','enterprise-2026-v3'))<>2
  OR (SELECT count(*) FROM edition_features WHERE feature_id='feat_0042')<>2 THEN
  RAISE EXCEPTION 'composition allocation incorrect';
 END IF;
 IF EXISTS(SELECT * FROM composition_versions_before EXCEPT SELECT * FROM edition_versions)
  OR EXISTS(SELECT * FROM composition_features_before EXCEPT SELECT * FROM edition_features)
  OR EXISTS(SELECT * FROM composition_limits_before EXCEPT SELECT * FROM edition_limits) THEN
  RAISE EXCEPTION 'historical configuration changed';
 END IF;
 IF EXISTS(SELECT 1 FROM edition_versions WHERE edition_version_id IN ('business-2026-v3','enterprise-2026-v3') AND NOT sealed)
  OR (SELECT count(*) FROM edition_limits WHERE edition_version_id IN ('business-2026-v3','enterprise-2026-v3'))<>16 THEN
  RAISE EXCEPTION 'unsealed or missing copied limits';
 END IF;
 IF EXISTS(
  SELECT split_part(edition_version_id,'-',1) AS edition,feature_id,enabled,limit_value,configuration_json
  FROM edition_features WHERE edition_version_id IN ('business-2026-v2','enterprise-2026-v2')
  EXCEPT
  SELECT split_part(edition_version_id,'-',1),feature_id,enabled,limit_value,configuration_json
  FROM edition_features WHERE edition_version_id IN ('business-2026-v3','enterprise-2026-v3')
 ) THEN RAISE EXCEPTION 'inherited features changed'; END IF;
 IF EXISTS(
  SELECT * FROM composition_customer_before
  EXCEPT
  (SELECT 'subscriptions',to_jsonb(t) FROM subscriptions t
   UNION ALL SELECT 'entitlements',to_jsonb(t) FROM entitlements t
   UNION ALL SELECT 'quota_usage',to_jsonb(t) FROM quota_usage t
   UNION ALL SELECT 'entitlement_limits',to_jsonb(t) FROM entitlement_limits t)
 ) OR (SELECT count(*) FROM composition_customer_before)<>
  ((SELECT count(*) FROM subscriptions)+(SELECT count(*) FROM entitlements)+(SELECT count(*) FROM quota_usage)+(SELECT count(*) FROM entitlement_limits)) THEN
  RAISE EXCEPTION 'customer state changed';
 END IF;
 BEGIN
  UPDATE edition_features SET enabled=false WHERE edition_version_id='business-2026-v3' AND feature_id='feat_0042';
  RAISE EXCEPTION 'sealed feature could change';
 EXCEPTION WHEN check_violation THEN NULL;
 END;
END;
$$;
