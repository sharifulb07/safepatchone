DO $$ BEGIN
 IF EXISTS(SELECT * FROM onboarding_data_before EXCEPT (
 SELECT 'repositories',to_jsonb(r) FROM repositories r
 UNION ALL SELECT 'subscriptions',to_jsonb(s) FROM subscriptions s
 UNION ALL SELECT 'members',to_jsonb(m) FROM organization_members m
 UNION ALL SELECT 'usage',to_jsonb(u) FROM quota_usage u
 UNION ALL SELECT 'entitlements',to_jsonb(e) FROM entitlements e
 UNION ALL SELECT 'bundles',to_jsonb(f) FROM edition_features f)) THEN
  RAISE EXCEPTION 'migration changed existing data';
 END IF;
END $$;
INSERT INTO onboarding_states VALUES ('onboard_a','onboard_user','attempt',NULL,1,'{"organization_id":"onboard_a","revision":1}');
INSERT INTO onboarding_github_connections VALUES ('onboard_a',7);
INSERT INTO onboarding_github_repositories VALUES ('onboard_a','repo',7,'acme/repo');
INSERT INTO onboarding_proof_jobs(organization_id,proof_id,actor_user_id,onboarding_revision,canonical_request,status)
VALUES('onboard_a','proof','onboard_user',1,'{"organization_id":"onboard_a"}','PENDING');
DO $$ BEGIN
 BEGIN
  INSERT INTO onboarding_states VALUES ('onboard_b','onboard_user','other',NULL,1,'{"organization_id":"onboard_a","revision":1}');
  RAISE EXCEPTION 'cross tenant canonical state accepted';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  INSERT INTO onboarding_states VALUES ('onboard_b','onboard_user','other',NULL,1,'{"revision":1}');
  RAISE EXCEPTION 'missing tenant accepted';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  INSERT INTO onboarding_github_connections VALUES ('onboard_b',7);
  RAISE EXCEPTION 'installation linked to another tenant';
 EXCEPTION WHEN unique_violation THEN NULL; END;
 BEGIN
  INSERT INTO onboarding_github_repositories VALUES ('onboard_b','repo',7,'acme/repo');
  RAISE EXCEPTION 'cross tenant repository accepted';
 EXCEPTION WHEN foreign_key_violation THEN NULL; END;
 BEGIN
  UPDATE onboarding_states SET revision=3,canonical_state='{"organization_id":"onboard_a","revision":3}';
  RAISE EXCEPTION 'revision jump accepted';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE onboarding_proof_jobs SET status='PASS';
  RAISE EXCEPTION 'PASS without evidence accepted';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE onboarding_proof_jobs SET actor_user_id='another_user';
  RAISE EXCEPTION 'worker actor substitution accepted';
 EXCEPTION WHEN check_violation THEN NULL; END;
END $$;
UPDATE onboarding_states SET requested_edition_version_id='team-2026-v2',revision=2,canonical_state='{"organization_id":"onboard_a","revision":2,"edition_version_id":"team-2026-v2"}';
UPDATE onboarding_proof_jobs SET status='UNKNOWN';
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM entitlements WHERE organization_id='onboard_a') THEN RAISE EXCEPTION 'selection granted access'; END IF;
 IF NOT EXISTS (SELECT 1 FROM subscriptions WHERE organization_id='onboard_a' AND edition_version_id='enterprise-2026-v1') THEN RAISE EXCEPTION 'selection changed paid source'; END IF;
 BEGIN
  UPDATE onboarding_proof_jobs SET status='PASS',evidence_reference='forged';
  RAISE EXCEPTION 'UNKNOWN promoted to PASS';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  DELETE FROM onboarding_states;
  RAISE EXCEPTION 'onboarding history removed';
 EXCEPTION WHEN check_violation THEN NULL; END;
END $$;
