INSERT INTO organizations VALUES ('autonomy_org','Autonomy','ACTIVE',now());
INSERT INTO users VALUES ('autonomy_user','User',true,now());
INSERT INTO organization_members VALUES ('autonomy_org','autonomy_user','owner',true,now());
INSERT INTO projects VALUES ('autonomy_org','project','Project',true,now());
INSERT INTO repositories VALUES ('autonomy_org','project','repo','Repo',true,now());
INSERT INTO subscriptions (organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
VALUES ('autonomy_org','sub_autonomy','enterprise-2026-v5','manual','contract','ACTIVE',1,now(),now());
INSERT INTO agent_safe_leases(organization_id,repository_id,lease_id,actor_id,actor_kind,capabilities,issued_at,expires_at,last_used_at,max_calls,uses,revoked)
VALUES('autonomy_org','repo','ordinary','autonomy_user','user','{"schema":"safepatch.agent.capabilities.v1","scope":{"organization_id":"autonomy_org","repository_id":"repo"},"lease_seconds":60,"call_budget":2}','2026-09-07T12:00:00Z','2026-09-07T12:01:00Z','2026-09-07T12:00:00Z',2,0,false);
CREATE TEMP TABLE autonomy_features_before AS SELECT * FROM edition_features;
CREATE TEMP TABLE autonomy_versions_before AS SELECT * FROM edition_versions;
CREATE TEMP TABLE autonomy_limits_before AS SELECT * FROM edition_limits;
CREATE TEMP TABLE autonomy_subscriptions_before AS SELECT * FROM subscriptions;
CREATE TEMP TABLE autonomy_entitlements_before AS SELECT * FROM entitlements;
CREATE TEMP TABLE autonomy_leases_before AS SELECT * FROM agent_safe_leases;
