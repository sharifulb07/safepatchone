DO $$ BEGIN
 IF (SELECT count(*) FROM editions)<>5 OR (SELECT count(*) FROM feature_identities)<>45 THEN RAISE EXCEPTION 'identity count'; END IF;
 IF (SELECT count(*) FROM edition_features WHERE feature_id='feat_0045' AND enabled)<>1
 OR NOT EXISTS(SELECT 1 FROM edition_features WHERE edition_version_id='enterprise-2026-v6' AND feature_id='feat_0045' AND enabled AND limit_value=32)
 OR NOT EXISTS(SELECT 1 FROM edition_features WHERE edition_version_id='enterprise-2026-v6' AND feature_id='feat_0021' AND enabled) THEN RAISE EXCEPTION 'allocation'; END IF;
 IF EXISTS(SELECT * FROM autonomy_versions_before EXCEPT SELECT * FROM edition_versions)
 OR EXISTS(SELECT * FROM autonomy_features_before EXCEPT SELECT * FROM edition_features)
 OR EXISTS(SELECT * FROM autonomy_limits_before EXCEPT SELECT * FROM edition_limits)
 OR EXISTS(SELECT * FROM autonomy_subscriptions_before EXCEPT SELECT * FROM subscriptions)
 OR EXISTS(SELECT * FROM subscriptions EXCEPT SELECT * FROM autonomy_subscriptions_before)
 OR EXISTS(SELECT * FROM autonomy_entitlements_before EXCEPT SELECT * FROM entitlements)
 OR EXISTS(SELECT * FROM entitlements EXCEPT SELECT * FROM autonomy_entitlements_before)
 OR EXISTS(SELECT * FROM autonomy_leases_before EXCEPT SELECT * FROM agent_safe_leases) THEN RAISE EXCEPTION 'history changed'; END IF;
END $$;
INSERT INTO agent_safe_leases(organization_id,repository_id,lease_id,actor_id,actor_kind,capabilities,issued_at,expires_at,last_used_at,max_calls,uses,revoked)
SELECT organization_id,repository_id,'earned',actor_id,actor_kind,
 (capabilities::jsonb || jsonb_build_object('earned',jsonb_build_object('policy_id','reviewed','challenge_digest',repeat('a',64),'proof_set_digest',repeat('b',64))))::text,
 issued_at,expires_at,last_used_at,max_calls,uses,revoked FROM agent_safe_leases WHERE lease_id='ordinary';
DO $$ DECLARE bad jsonb; BEGIN
 FOR bad IN SELECT value FROM jsonb_array_elements('[null,{},true,{"policy_id":"reviewed"},{"policy_id":"../escape","challenge_digest":"bad","proof_set_digest":"bad"}]') LOOP
  BEGIN
   INSERT INTO agent_safe_leases(organization_id,repository_id,lease_id,actor_id,actor_kind,capabilities,issued_at,expires_at,last_used_at,max_calls,uses,revoked)
   SELECT organization_id,repository_id,'invalid',actor_id,actor_kind,(capabilities::jsonb || jsonb_build_object('earned',bad))::text,issued_at,expires_at,last_used_at,max_calls,uses,revoked FROM agent_safe_leases WHERE lease_id='ordinary';
   RAISE EXCEPTION 'invalid proof binding';
  EXCEPTION WHEN check_violation THEN NULL; END;
 END LOOP;
 BEGIN
  UPDATE agent_safe_leases SET capabilities=(capabilities::jsonb-'earned')::text WHERE lease_id='earned';
  RAISE EXCEPTION 'earned authority stripped';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE edition_features SET limit_value=100 WHERE edition_version_id='enterprise-2026-v6' AND feature_id='feat_0045';
  RAISE EXCEPTION 'sealed allocation modified';
 EXCEPTION WHEN check_violation THEN NULL; END;
END $$;
