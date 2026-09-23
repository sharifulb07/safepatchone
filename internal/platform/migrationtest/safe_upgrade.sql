DO $$
BEGIN
 IF (SELECT count(*) FROM editions)<>5 OR (SELECT count(*) FROM feature_identities)<>44 OR (SELECT count(*) FROM quota_limit_keys)<>11 THEN RAISE EXCEPTION 'identity count mismatch'; END IF;
 IF (SELECT count(*) FROM edition_features WHERE feature_id='feat_0044' AND enabled)<>4 THEN RAISE EXCEPTION 'allocation mismatch'; END IF;
 IF NOT EXISTS(SELECT 1 FROM edition_limits WHERE edition_version_id='developer-2026-v3' AND limit_key='safe_upgrades_monthly' AND state='INTEGER' AND value=10)
 OR NOT EXISTS(SELECT 1 FROM edition_limits WHERE edition_version_id='team-2026-v3' AND limit_key='safe_upgrades_monthly' AND state='INTEGER' AND value=100)
 OR NOT EXISTS(SELECT 1 FROM edition_limits WHERE edition_version_id='business-2026-v4' AND limit_key='safe_upgrades_monthly' AND state='INTEGER' AND value=1000)
 OR NOT EXISTS(SELECT 1 FROM edition_limits WHERE edition_version_id='enterprise-2026-v5' AND limit_key='safe_upgrades_monthly' AND state='UNLIMITED' AND value=0) THEN RAISE EXCEPTION 'limit mapping mismatch'; END IF;
 IF EXISTS(SELECT * FROM upgrade_versions_before EXCEPT SELECT * FROM edition_versions)
 OR EXISTS(SELECT * FROM upgrade_features_before EXCEPT SELECT * FROM edition_features)
 OR EXISTS(SELECT * FROM upgrade_limits_before EXCEPT SELECT * FROM edition_limits) THEN RAISE EXCEPTION 'bundle history changed'; END IF;
 IF EXISTS(SELECT * FROM upgrade_customer_before EXCEPT
  (SELECT 'subscriptions',to_jsonb(t) FROM subscriptions t
   UNION ALL SELECT 'entitlements',to_jsonb(t) FROM entitlements t
   UNION ALL SELECT 'quota_usage',to_jsonb(t) FROM quota_usage t
   UNION ALL SELECT 'quota_reservations',to_jsonb(t) FROM quota_reservations t
   UNION ALL SELECT 'entitlement_limits',to_jsonb(t) FROM entitlement_limits t)) THEN RAISE EXCEPTION 'customer data changed'; END IF;
 INSERT INTO quota_usage VALUES ('org_upgrade','safe_upgrades_monthly','2026-09',0,1);
 INSERT INTO quota_reservations VALUES ('org_upgrade','upgrade','safe_upgrades_monthly','2026-09',1,'COMMITTED');
 BEGIN
  UPDATE quota_reservations SET state='RELEASED' WHERE organization_id='org_upgrade' AND reservation_id='upgrade';
  RAISE EXCEPTION 'monthly upgrade refund allowed';
 EXCEPTION WHEN check_violation THEN NULL;
 END;
 BEGIN
  UPDATE quota_reservations SET state='RELEASED' WHERE organization_id='org_upgrade' AND reservation_id='prior';
  RAISE EXCEPTION 'historical monthly refund allowed';
 EXCEPTION WHEN check_violation THEN NULL;
 END;
 BEGIN
  INSERT INTO quota_usage VALUES ('org_upgrade','safe_upgrades_monthly','lifetime',0,1);
  RAISE EXCEPTION 'invalid upgrade period accepted';
 EXCEPTION WHEN check_violation THEN NULL;
 END;
 BEGIN
  UPDATE edition_limits SET value=999 WHERE edition_version_id='developer-2026-v3' AND limit_key='safe_upgrades_monthly';
  RAISE EXCEPTION 'sealed limit mutation allowed';
 EXCEPTION WHEN check_violation THEN NULL;
 END;
END;
$$;
