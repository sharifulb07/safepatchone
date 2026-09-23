INSERT INTO entitlement_limits VALUES ('replay_org','replay_concurrency','INTEGER',1),('replay_org','dataset_retention_days','INTEGER',7);
INSERT INTO quota_usage VALUES ('replay_org','replay_concurrency','lifetime',1,0);
INSERT INTO quota_reservations VALUES ('replay_org','replay_slot','replay_concurrency','lifetime',1,'RESERVED');
INSERT INTO replay_datasets VALUES ('replay_org','repo','dataset',repeat('a',64),100,repeat('b',64),'2026-09-07T00:00:00Z','2026-09-14T00:00:00Z');
INSERT INTO replay_runs(organization_id,repository_id,run_id,dataset_digest,decision) VALUES('replay_org','repo','run',repeat('a',64),'UNKNOWN');
DO $$ BEGIN
 BEGIN
  INSERT INTO quota_usage VALUES ('replay_org','replay_concurrency','2026-09',1,0);
  RAISE EXCEPTION 'replay capacity used monthly period';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  INSERT INTO replay_runs(organization_id,repository_id,run_id,dataset_digest,decision) VALUES('replay_other','repo','run',repeat('a',64),'UNKNOWN');
  RAISE EXCEPTION 'cross tenant dataset replayed';
 EXCEPTION WHEN foreign_key_violation THEN NULL; END;
 BEGIN
  INSERT INTO replay_runs(organization_id,repository_id,run_id,dataset_digest,decision) VALUES('replay_org','repo','run',repeat('a',64),'UNKNOWN');
  RAISE EXCEPTION 'run executed twice';
 EXCEPTION WHEN unique_violation THEN NULL; END;
 BEGIN
  UPDATE replay_datasets SET expires_at=expires_at+interval '1 day';
  RAISE EXCEPTION 'dataset retention extended';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE replay_runs SET decision='MATCH';
  RAISE EXCEPTION 'UNKNOWN promoted without evidence';
 EXCEPTION WHEN check_violation THEN NULL; END;
END $$;
UPDATE replay_runs SET decision='DIFFERENT',result=jsonb_build_object('dataset_digest',repeat('a',64),'decision','DIFFERENT','cases','[]'::jsonb)::text;
UPDATE quota_usage SET reserved=0 WHERE limit_key='replay_concurrency';
UPDATE quota_reservations SET state='RELEASED' WHERE reservation_id='replay_slot';
UPDATE entitlement_limits SET value=0 WHERE limit_key='replay_concurrency';
DO $$ BEGIN
 BEGIN
  UPDATE replay_runs SET decision='MATCH',result=jsonb_build_object('dataset_digest',repeat('a',64),'decision','MATCH','cases','[]'::jsonb)::text;
  RAISE EXCEPTION 'completed comparison changed';
 EXCEPTION WHEN check_violation THEN NULL; END;
 IF NOT EXISTS(SELECT 1 FROM replay_datasets WHERE dataset_id='dataset' AND digest=repeat('a',64)) THEN RAISE EXCEPTION 'downgrade corrupted dataset'; END IF;
 IF EXISTS(SELECT * FROM replay_subscriptions_before EXCEPT SELECT * FROM subscriptions)
 OR EXISTS(SELECT * FROM subscriptions EXCEPT SELECT * FROM replay_subscriptions_before)
 OR EXISTS(SELECT * FROM replay_limits_before EXCEPT SELECT * FROM edition_limits)
 OR EXISTS(SELECT * FROM edition_limits EXCEPT SELECT * FROM replay_limits_before) THEN
 RAISE EXCEPTION 'published commercial state changed'; END IF;
END $$;
