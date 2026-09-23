INSERT INTO organizations VALUES ('trial_a','A','ACTIVE','2026-09-07T12:00:00Z'),('trial_b','B','ACTIVE','2026-09-07T12:00:00Z'),('trial_c','C','ACTIVE','2026-09-07T12:00:00Z');
INSERT INTO users VALUES ('trial_user','User',true,'2026-09-07T12:00:00Z');
INSERT INTO organization_members VALUES ('trial_a','trial_user','owner',true,'2026-09-07T12:00:00Z'),('trial_b','trial_user','viewer',true,'2026-09-07T12:00:00Z');
INSERT INTO projects VALUES ('trial_a','project','Project',true,'2026-09-07T12:00:00Z');
INSERT INTO repositories VALUES ('trial_a','project','repo','Repo',true,'2026-09-07T12:00:00Z');
INSERT INTO quota_accounts VALUES ('trial_a');
INSERT INTO quota_usage VALUES ('trial_a','proof_credits_monthly','2026-09',2,3);
INSERT INTO quota_reservations VALUES ('trial_a','existing_hold','proof_credits_monthly','2026-09',2,'RESERVED');
INSERT INTO subscriptions(organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
VALUES('trial_a','manual_contract','enterprise-2026-v1','manual','contract_external','ACTIVE',1,'2026-09-07T12:00:00Z','2026-09-07T12:00:00Z');
INSERT INTO entitlements(organization_id,entitlement_id,feature_id,source,source_reference,canonical_record)
VALUES('trial_b','legacy_trial','feat_0011','TRIAL','legacy:opaque',jsonb_build_object(
 'organization_id','trial_b','entitlement_id','legacy_trial','feature_id','feat_0011','source','TRIAL','source_reference','legacy:opaque',
 'enabled',true,'limit_value',null,'starts_at','2026-01-01T00:00:00Z','expires_at','2026-01-15T00:00:00Z','metadata','{}'::jsonb,'created_at','2026-01-01T00:00:00Z')::text);
CREATE TEMP TABLE trial_data_before AS SELECT 'repositories' AS kind,to_jsonb(r) AS value FROM repositories r
 UNION ALL SELECT 'subscriptions',to_jsonb(s) FROM subscriptions s
 UNION ALL SELECT 'members',to_jsonb(m) FROM organization_members m
 UNION ALL SELECT 'usage',to_jsonb(u) FROM quota_usage u
 UNION ALL SELECT 'reservations',to_jsonb(r) FROM quota_reservations r
 UNION ALL SELECT 'entitlements',to_jsonb(e) FROM entitlements e
 UNION ALL SELECT 'bundles',to_jsonb(f) FROM edition_features f;
