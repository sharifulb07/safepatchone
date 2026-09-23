DO $$
BEGIN
 IF (SELECT count(*) FROM editions)<>5 OR (SELECT count(*) FROM feature_identities)<>43
  OR NOT EXISTS(SELECT 1 FROM feature_identities WHERE feature_id='feat_0043' AND key='graph.global_change') THEN
  RAISE EXCEPTION 'global change identity mismatch';
 END IF;
 IF (SELECT count(*) FROM edition_features WHERE feature_id='feat_0043')<>1
  OR NOT EXISTS(SELECT 1 FROM edition_features WHERE feature_id='feat_0043' AND edition_version_id='enterprise-2026-v4' AND enabled) THEN
  RAISE EXCEPTION 'global change allocation mismatch';
 END IF;
 IF EXISTS(SELECT * FROM global_versions_before EXCEPT SELECT * FROM edition_versions)
  OR EXISTS(SELECT * FROM global_features_before EXCEPT SELECT * FROM edition_features)
  OR EXISTS(SELECT * FROM global_limits_before EXCEPT SELECT * FROM edition_limits) THEN
  RAISE EXCEPTION 'historical bundle changed';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM edition_versions WHERE edition_version_id='enterprise-2026-v4' AND sealed)
  OR (SELECT count(*) FROM edition_limits WHERE edition_version_id='enterprise-2026-v4')<>8 THEN
  RAISE EXCEPTION 'unsealed bundle or missing limits';
 END IF;
 IF EXISTS(SELECT feature_id,enabled,limit_value,configuration_json FROM edition_features WHERE edition_version_id='enterprise-2026-v3'
  EXCEPT SELECT feature_id,enabled,limit_value,configuration_json FROM edition_features WHERE edition_version_id='enterprise-2026-v4') THEN
  RAISE EXCEPTION 'inherited features changed';
 END IF;
 IF EXISTS(SELECT * FROM global_customer_before EXCEPT
  (SELECT 'subscriptions',to_jsonb(t) FROM subscriptions t
   UNION ALL SELECT 'entitlements',to_jsonb(t) FROM entitlements t
   UNION ALL SELECT 'quota_usage',to_jsonb(t) FROM quota_usage t
   UNION ALL SELECT 'entitlement_limits',to_jsonb(t) FROM entitlement_limits t))
  OR (SELECT count(*) FROM global_customer_before)<>
   ((SELECT count(*) FROM subscriptions)+(SELECT count(*) FROM entitlements)+(SELECT count(*) FROM quota_usage)+(SELECT count(*) FROM entitlement_limits)) THEN
  RAISE EXCEPTION 'customer state changed';
 END IF;
 BEGIN
  UPDATE edition_features SET enabled=false WHERE edition_version_id='enterprise-2026-v4' AND feature_id='feat_0043';
  RAISE EXCEPTION 'sealed bundle is mutable';
 EXCEPTION WHEN check_violation THEN NULL;
 END;
END;
$$;
