INSERT INTO billing_connections VALUES('stripe_test','stripe','acct_test',false),('stripe_live','stripe','acct_test',true);
INSERT INTO billing_customers VALUES('stripe_test','bill_a','cus_a'),('stripe_live','bill_b','cus_a');
INSERT INTO billing_events(connection_id,event_id,organization_id,external_reference,occurred_at)
VALUES('stripe_test','evt_first','bill_a','sub_external','2026-09-07T12:00:00Z');
-- Same identity is idempotent; a second connection has an independent namespace.
INSERT INTO billing_events(connection_id,event_id,organization_id,external_reference,occurred_at)
VALUES('stripe_test','evt_first','bill_a','sub_external','2026-09-07T12:00:00Z') ON CONFLICT(connection_id,event_id) DO NOTHING;
INSERT INTO billing_events(connection_id,event_id,organization_id,external_reference,occurred_at)
VALUES('stripe_live','evt_first','bill_b','different','2026-09-07T12:00:00Z');
INSERT INTO subscriptions(organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
VALUES('bill_a','local_subscription','team-2026-v2','stripe','sub_external','ACTIVE',1,'2026-09-07T12:00:00Z','2026-09-07T12:00:00Z');
INSERT INTO billing_subscription_state VALUES('bill_a','local_subscription','stripe_test',1,'2026-10-07T12:00:00Z',true,false);
INSERT INTO billing_subscription_history VALUES('bill_a','local_subscription',1,'stripe_test','evt_first','{}');
UPDATE billing_events SET applied_at='2026-09-07T12:01:00Z' WHERE connection_id='stripe_test' AND event_id='evt_first';
DO $$
DECLARE statement text;
BEGIN
 IF (SELECT count(*) FROM billing_events WHERE connection_id='stripe_test')<>1 THEN RAISE EXCEPTION 'duplicate receipt'; END IF;
 BEGIN
  INSERT INTO billing_subscription_state VALUES('bill_b','local_subscription','stripe_live',1,NULL,false,false);
  RAISE EXCEPTION 'foreign subscription accepted';
 EXCEPTION WHEN foreign_key_violation THEN NULL; END;
 BEGIN
  INSERT INTO billing_subscription_history VALUES('bill_a','local_subscription',2,'stripe_live','evt_first','{}');
  RAISE EXCEPTION 'foreign receipt attached';
 EXCEPTION WHEN foreign_key_violation THEN NULL; END;
 FOREACH statement IN ARRAY ARRAY[
 'UPDATE billing_connections SET live_mode=true WHERE connection_id=''stripe_test''',
 'UPDATE billing_customers SET organization_id=''bill_b'' WHERE connection_id=''stripe_test''',
 'UPDATE billing_events SET organization_id=''bill_b'' WHERE connection_id=''stripe_test''',
 'UPDATE billing_events SET applied_at=NULL WHERE connection_id=''stripe_test''',
 'DELETE FROM billing_events', 'TRUNCATE billing_events CASCADE',
 'UPDATE billing_subscription_state SET revision=1 WHERE organization_id=''bill_a''',
 'UPDATE billing_subscription_state SET connection_id=''stripe_live'',revision=2 WHERE organization_id=''bill_a''',
 'DELETE FROM billing_subscription_history'] LOOP
  BEGIN EXECUTE statement; RAISE EXCEPTION 'billing invariant bypass';
  EXCEPTION WHEN check_violation THEN NULL; END;
 END LOOP;
 -- A failed receipt transaction must not leave a changed subscription.
 BEGIN
  UPDATE subscriptions SET state='CANCELED',revision=2 WHERE organization_id='bill_a';
  UPDATE billing_subscription_state SET revision=2,access_until=NULL WHERE organization_id='bill_a';
  RAISE EXCEPTION 'simulated crash' USING ERRCODE='P0002';
 EXCEPTION WHEN no_data_found THEN NULL; END;
 IF (SELECT state FROM subscriptions WHERE organization_id='bill_a')<>'ACTIVE' OR (SELECT revision FROM billing_subscription_state WHERE organization_id='bill_a')<>1 THEN RAISE EXCEPTION 'partial synchronization'; END IF;
END; $$;
-- Upgrade/downgrade/cancel affects assignment and observation, never existing data.
UPDATE subscriptions SET edition_version_id='developer-2026-v2',revision=2 WHERE organization_id='bill_a';
UPDATE billing_subscription_state SET revision=2 WHERE organization_id='bill_a';
UPDATE subscriptions SET edition_version_id='business-2026-v2',revision=3 WHERE organization_id='bill_a';
UPDATE billing_subscription_state SET revision=3 WHERE organization_id='bill_a';
UPDATE subscriptions SET state='CANCELED',revision=4 WHERE organization_id='bill_a';
UPDATE billing_subscription_state SET revision=4,access_until=NULL,payment_settled=false WHERE organization_id='bill_a';
DO $$
BEGIN
 IF EXISTS((SELECT * FROM bill_usage_before EXCEPT SELECT * FROM quota_usage) UNION ALL (SELECT * FROM quota_usage EXCEPT SELECT * FROM bill_usage_before))
 OR EXISTS((SELECT * FROM bill_reservations_before EXCEPT SELECT * FROM quota_reservations) UNION ALL (SELECT * FROM quota_reservations EXCEPT SELECT * FROM bill_reservations_before))
 OR EXISTS((SELECT * FROM bill_repositories_before EXCEPT SELECT * FROM repositories) UNION ALL (SELECT * FROM repositories EXCEPT SELECT * FROM bill_repositories_before))
 OR EXISTS((SELECT * FROM bill_members_before EXCEPT SELECT * FROM organization_members) UNION ALL (SELECT * FROM organization_members EXCEPT SELECT * FROM bill_members_before))
 OR EXISTS((SELECT * FROM bill_entitlements_before EXCEPT SELECT * FROM entitlements) UNION ALL (SELECT * FROM entitlements EXCEPT SELECT * FROM bill_entitlements_before))
 OR EXISTS((SELECT * FROM bill_features_before EXCEPT SELECT * FROM edition_features) UNION ALL (SELECT * FROM edition_features EXCEPT SELECT * FROM bill_features_before))
 OR EXISTS((SELECT * FROM bill_limits_before EXCEPT SELECT * FROM edition_limits) UNION ALL (SELECT * FROM edition_limits EXCEPT SELECT * FROM bill_limits_before)) THEN RAISE EXCEPTION 'plan change corrupted history'; END IF;
 IF (SELECT state FROM subscriptions WHERE subscription_id='manual_contract')<>'ACTIVE' THEN RAISE EXCEPTION 'manual contract changed'; END IF;
END; $$;
