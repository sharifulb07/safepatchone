INSERT INTO agent_safe_leases(organization_id,repository_id,lease_id,actor_id,actor_kind,capabilities,issued_at,expires_at,last_used_at,max_calls,uses,revoked)
VALUES('agent_org','repo','lease','agent_user','user','{"schema":"safepatch.agent.capabilities.v1","scope":{"organization_id":"agent_org","repository_id":"repo"},"lease_seconds":60,"call_budget":2}','2026-09-07T12:00:00Z','2026-09-07T12:01:00Z','2026-09-07T12:00:00Z',2,0,false);
DO $$ BEGIN
 BEGIN
  UPDATE agent_safe_leases SET organization_id='agent_other';
  RAISE EXCEPTION 'tenant identity changed';
 EXCEPTION WHEN check_violation OR foreign_key_violation THEN NULL; END;
 BEGIN
  UPDATE agent_safe_leases SET expires_at=expires_at+interval '1 hour';
  RAISE EXCEPTION 'lease extended';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE agent_safe_leases SET uses=3;
  RAISE EXCEPTION 'budget bypassed';
 EXCEPTION WHEN check_violation THEN NULL; END;
END $$;
UPDATE agent_safe_leases SET uses=1,last_used_at='2026-09-07T12:00:01Z';
INSERT INTO agent_invocations VALUES('agent_org','repo','lease','call',repeat('a',64),'UNKNOWN','',false);
DO $$ BEGIN
 BEGIN
  INSERT INTO agent_invocations VALUES('agent_org','repo','lease','call',repeat('a',64),'UNKNOWN','',false);
  RAISE EXCEPTION 'invocation replay accepted';
 EXCEPTION WHEN unique_violation THEN NULL; END;
 BEGIN
  INSERT INTO agent_invocations VALUES('agent_other','repo','lease','call',repeat('a',64),'UNKNOWN','',false);
  RAISE EXCEPTION 'foreign invocation accepted';
 EXCEPTION WHEN foreign_key_violation THEN NULL; END;
 BEGIN
  UPDATE agent_safe_leases SET uses=0;
  RAISE EXCEPTION 'budget refunded';
 EXCEPTION WHEN check_violation THEN NULL; END;
END $$;
UPDATE agent_invocations SET outcome='RETURNED',result_digest=repeat('b',64),finished=true;
UPDATE agent_safe_leases SET revoked=true;
DO $$ BEGIN
 BEGIN
  UPDATE agent_invocations SET outcome='TOOL_ERROR',result_digest=repeat('c',64);
  RAISE EXCEPTION 'result rewritten';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE agent_safe_leases SET revoked=false;
  RAISE EXCEPTION 'lease resurrected';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  TRUNCATE agent_invocations;
  RAISE EXCEPTION 'audit removed';
 EXCEPTION WHEN check_violation THEN NULL; END;
 IF EXISTS(SELECT * FROM agent_subscriptions_before EXCEPT SELECT * FROM subscriptions)
 OR EXISTS(SELECT * FROM subscriptions EXCEPT SELECT * FROM agent_subscriptions_before)
 OR EXISTS(SELECT * FROM agent_entitlements_before EXCEPT SELECT * FROM entitlements)
 OR EXISTS(SELECT * FROM entitlements EXCEPT SELECT * FROM agent_entitlements_before) THEN
 RAISE EXCEPTION 'commercial state changed'; END IF;
END $$;
