-- Edition labels intentionally do not participate in any tenant constraint.
-- Two distinct organizations exist even for Enterprise -> Enterprise.
DO $$
DECLARE
 a text; b text; attacker text; victim text; caps text;
 editions text[] := ARRAY['Community','Developer','Team','Business','Enterprise'];
 stamp timestamptz := '2026-09-07T12:00:00Z';
BEGIN
 FOREACH a IN ARRAY editions LOOP
  FOREACH b IN ARRAY editions LOOP
   attacker := 'attacker_' || a || '_' || b;
   victim := 'victim_' || a || '_' || b;
   INSERT INTO organizations VALUES(attacker,attacker,'ACTIVE',stamp),(victim,victim,'ACTIVE',stamp);
   INSERT INTO projects VALUES(attacker,'shared_project','Project',true,stamp),(victim,'shared_project','Project',true,stamp),(victim,'private_project','Private',true,stamp);
   INSERT INTO repositories VALUES(attacker,'shared_project','shared_repo','Repository',true,stamp),(victim,'shared_project','shared_repo','Repository',true,stamp);
   BEGIN
    INSERT INTO repositories VALUES(attacker,'private_project','stolen','Repository',true,stamp);
    RAISE EXCEPTION 'foreign project accepted: % -> %',a,b;
   EXCEPTION WHEN foreign_key_violation THEN NULL; END;

   INSERT INTO replay_datasets VALUES(victim,'shared_repo','same_dataset',repeat('a',64),2,repeat('b',64),stamp,stamp+interval '1 day');
   BEGIN
    INSERT INTO replay_runs VALUES(attacker,'shared_repo','same_run',repeat('a',64),'UNKNOWN',NULL);
    RAISE EXCEPTION 'foreign replay dataset accepted: % -> %',a,b;
   EXCEPTION WHEN foreign_key_violation THEN NULL; END;
   INSERT INTO replay_datasets VALUES(attacker,'shared_repo','same_dataset',repeat('c',64),2,repeat('b',64),stamp,stamp+interval '1 day');
   INSERT INTO replay_runs VALUES(attacker,'shared_repo','same_run',repeat('c',64),'UNKNOWN',NULL),(victim,'shared_repo','same_run',repeat('a',64),'UNKNOWN',NULL);
   BEGIN
    UPDATE replay_runs SET organization_id=attacker WHERE organization_id=victim AND run_id='same_run';
    RAISE EXCEPTION 'replay ownership rewritten: % -> %',a,b;
   EXCEPTION WHEN check_violation THEN NULL; END;

   caps := jsonb_build_object('schema','safepatch.agent.capabilities.v1','scope',jsonb_build_object('organization_id',victim,'repository_id','shared_repo'),'lease_seconds',60,'call_budget',2)::text;
   INSERT INTO agent_safe_leases(organization_id,repository_id,lease_id,actor_id,actor_kind,capabilities,issued_at,expires_at,last_used_at,max_calls,uses,revoked)
   VALUES(victim,'shared_repo','same_lease','worker','service_account',caps,stamp,stamp+interval '60 seconds',stamp,2,0,false);
   BEGIN
    INSERT INTO agent_invocations VALUES(attacker,'shared_repo','same_lease','same_call',repeat('d',64),'UNKNOWN','',false);
    RAISE EXCEPTION 'foreign MCP lease accepted: % -> %',a,b;
   EXCEPTION WHEN foreign_key_violation THEN NULL; END;
   BEGIN
    INSERT INTO agent_safe_leases(organization_id,repository_id,lease_id,actor_id,actor_kind,capabilities,issued_at,expires_at,last_used_at,max_calls,uses,revoked)
    VALUES(attacker,'shared_repo','same_lease','worker','service_account',caps,stamp,stamp+interval '60 seconds',stamp,2,0,false);
    RAISE EXCEPTION 'foreign capability document accepted: % -> %',a,b;
   EXCEPTION WHEN check_violation THEN NULL; END;
   INSERT INTO agent_invocations VALUES(victim,'shared_repo','same_lease','same_call',repeat('d',64),'UNKNOWN','',false);
   IF (SELECT count(*) FROM replay_datasets WHERE organization_id=victim AND digest=repeat('a',64))<>1
   OR (SELECT count(*) FROM agent_safe_leases WHERE organization_id=victim AND uses=0 AND NOT revoked)<>1 THEN
    RAISE EXCEPTION 'victim data changed: % -> %',a,b;
   END IF;
  END LOOP;
 END LOOP;
END $$;
