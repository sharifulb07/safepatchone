INSERT INTO license_installations VALUES('license_org',1,false,convert_to('signed fixture','UTF8'),'2026-09-07T00:00:00Z',false);
UPDATE license_installations SET revision=2,confirmed_at='2026-09-07T01:00:00Z' WHERE organization_id='license_org' AND revision=1;
DO $$ BEGIN
 BEGIN
  INSERT INTO license_installations VALUES('missing_org',1,false,convert_to('fixture','UTF8'),NULL,false);
  RAISE EXCEPTION 'unknown tenant installed';
 EXCEPTION WHEN foreign_key_violation THEN NULL; END;
 BEGIN
  UPDATE license_installations SET organization_id='license_other',revision=3;
  RAISE EXCEPTION 'license moved across tenants';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE license_installations SET confirmed_at='2026-09-07T02:00:00Z';
  RAISE EXCEPTION 'revision not advanced';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE license_installations SET revision=3,confirmed_at='2026-09-07T00:00:00Z';
  RAISE EXCEPTION 'anchor regressed';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE license_installations SET revision=3,confirmed_at=NULL;
  RAISE EXCEPTION 'anchor erased';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  DELETE FROM license_installations;
  RAISE EXCEPTION 'installation reset';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  TRUNCATE license_installations;
  RAISE EXCEPTION 'installation truncated';
 EXCEPTION WHEN check_violation THEN NULL; END;
 UPDATE license_installations SET revision=3,revoked=true WHERE organization_id='license_org' AND revision=1;
 IF FOUND THEN RAISE EXCEPTION 'stale refresh overwrote installation'; END IF;
 IF EXISTS(SELECT * FROM license_subscriptions_before EXCEPT SELECT * FROM subscriptions)
 OR EXISTS(SELECT * FROM subscriptions EXCEPT SELECT * FROM license_subscriptions_before)
 OR EXISTS(SELECT * FROM license_limits_before EXCEPT SELECT * FROM edition_limits)
 OR EXISTS(SELECT * FROM edition_limits EXCEPT SELECT * FROM license_limits_before)
 OR EXISTS(SELECT * FROM license_usage_before EXCEPT SELECT * FROM quota_usage)
 OR EXISTS(SELECT * FROM quota_usage EXCEPT SELECT * FROM license_usage_before) THEN
 RAISE EXCEPTION 'existing commercial state changed'; END IF;
END $$;
