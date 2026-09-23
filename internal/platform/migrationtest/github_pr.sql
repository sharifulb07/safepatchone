INSERT INTO github_pr_bindings VALUES ('github_a','gh_1','github_user',true,repeat('d',64));
INSERT INTO github_pr_jobs(job_id,organization_id,repository_id,canonical_job,status)
VALUES('pr_'||repeat('a',64),'github_a','gh_1',jsonb_build_object('id','pr_'||repeat('a',64),'binding',jsonb_build_object('OrganizationID','github_a','RepositoryID','gh_1'))::text,'PENDING');
INSERT INTO github_pr_deliveries VALUES ('delivery',repeat('b',64),'pr_'||repeat('a',64),now());
DO $$ BEGIN
 IF EXISTS(SELECT * FROM github_subscription_before EXCEPT SELECT * FROM subscriptions) THEN RAISE EXCEPTION 'subscription data changed'; END IF;
 IF EXISTS(SELECT 1 FROM entitlements WHERE organization_id='github_a') THEN RAISE EXCEPTION 'PR enabled entitlement'; END IF;
 BEGIN
  INSERT INTO github_pr_bindings VALUES ('github_b','gh_1','github_user',true,repeat('d',64));
  RAISE EXCEPTION 'cross tenant binding accepted';
 EXCEPTION WHEN foreign_key_violation THEN NULL; END;
 BEGIN
  INSERT INTO github_pr_deliveries VALUES ('delivery',repeat('c',64),'pr_'||repeat('a',64),now());
  RAISE EXCEPTION 'delivery replay overwrote identity';
 EXCEPTION WHEN unique_violation THEN NULL; END;
 BEGIN
  UPDATE github_pr_deliveries SET payload_hash=repeat('c',64);
  RAISE EXCEPTION 'receipt changed';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE github_pr_jobs SET canonical_job='{}';
  RAISE EXCEPTION 'job lost tenant identity';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE github_pr_jobs SET status='DONE';
  RAISE EXCEPTION 'pending job skipped evidence';
 EXCEPTION WHEN check_violation THEN NULL; END;
END $$;
UPDATE github_pr_jobs SET status='RUNNING',lease_id='lease';
UPDATE github_pr_jobs SET status='READY',canonical_result='{"Decision":"UNKNOWN"}',lease_id=NULL;
UPDATE github_pr_jobs SET status='DONE';
DO $$ BEGIN
 BEGIN
  UPDATE github_pr_jobs SET canonical_result='{"Decision":"PASS"}';
  RAISE EXCEPTION 'UNKNOWN result promoted to PASS';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  UPDATE github_pr_jobs SET status='PENDING';
  RAISE EXCEPTION 'completed job resurrected';
 EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN
  DELETE FROM github_pr_jobs;
  RAISE EXCEPTION 'job history deleted';
 EXCEPTION WHEN check_violation THEN NULL; END;
END $$;
