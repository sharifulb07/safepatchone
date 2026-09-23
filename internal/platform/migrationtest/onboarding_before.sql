INSERT INTO organizations VALUES ('onboard_a','A','ACTIVE',now()),('onboard_b','B','ACTIVE',now());
INSERT INTO users VALUES ('onboard_user','User',true,now());
INSERT INTO organization_members VALUES ('onboard_a','onboard_user','owner',true,now()),('onboard_b','onboard_user','viewer',true,now());
INSERT INTO projects VALUES ('onboard_a','project','Project',true,now());
INSERT INTO repositories VALUES ('onboard_a','project','repo','Repo',true,now());
INSERT INTO quota_accounts VALUES ('onboard_a');
INSERT INTO quota_usage VALUES ('onboard_a','proof_credits_monthly','2026-09',2,3);
INSERT INTO subscriptions(organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
VALUES('onboard_a','manual_contract','enterprise-2026-v1','manual','contract_external','ACTIVE',1,now(),now());
CREATE TEMP TABLE onboarding_data_before AS SELECT 'repositories' AS kind,to_jsonb(r) AS value FROM repositories r
 UNION ALL SELECT 'subscriptions',to_jsonb(s) FROM subscriptions s
 UNION ALL SELECT 'members',to_jsonb(m) FROM organization_members m
 UNION ALL SELECT 'usage',to_jsonb(u) FROM quota_usage u
 UNION ALL SELECT 'entitlements',to_jsonb(e) FROM entitlements e
 UNION ALL SELECT 'bundles',to_jsonb(f) FROM edition_features f;
