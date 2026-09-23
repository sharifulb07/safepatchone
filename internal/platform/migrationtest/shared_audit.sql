INSERT INTO audit_events VALUES('audit_org','event','{"organization_id":"audit_org","id":"event"}');
DO $$ BEGIN
 BEGIN
  INSERT INTO audit_events VALUES('audit_other','event','{"organization_id":"audit_org","id":"event"}');
  RAISE EXCEPTION 'cross tenant audit accepted';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  INSERT INTO audit_events VALUES('missing','event','{"organization_id":"missing","id":"event"}');
  RAISE EXCEPTION 'missing tenant accepted';
 EXCEPTION WHEN foreign_key_violation THEN NULL; END;
 BEGIN
  UPDATE audit_events SET record='{"organization_id":"audit_org","id":"event","tampered":true}';
  RAISE EXCEPTION 'audit changed';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  DELETE FROM audit_events;
  RAISE EXCEPTION 'audit erased';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  TRUNCATE audit_events;
  RAISE EXCEPTION 'audit truncated';
 EXCEPTION WHEN check_violation THEN NULL; END;
 IF EXISTS(SELECT * FROM audit_subscriptions_before EXCEPT SELECT * FROM subscriptions)
 OR EXISTS(SELECT * FROM subscriptions EXCEPT SELECT * FROM audit_subscriptions_before)
 OR EXISTS(SELECT * FROM audit_editions_before EXCEPT SELECT * FROM edition_limits)
 OR EXISTS(SELECT * FROM edition_limits EXCEPT SELECT * FROM audit_editions_before) THEN
 RAISE EXCEPTION 'commercial history changed'; END IF;
END $$;
