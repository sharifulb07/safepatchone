INSERT INTO organizations (organization_id,name,state,created_at)
VALUES ('org_upgrade','Upgrade fixture','ACTIVE','2026-09-07T00:00:00Z');
INSERT INTO quota_accounts (organization_id) VALUES ('org_upgrade');
INSERT INTO subscriptions (organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
VALUES ('org_upgrade','sub_upgrade','enterprise-2026-v4','manual','upgrade_contract','ACTIVE',1,'2026-09-07T00:00:00Z','2026-09-07T00:00:00Z');
INSERT INTO quota_usage VALUES ('org_upgrade','proof_credits_monthly','2026-09',0,1);
INSERT INTO quota_reservations VALUES ('org_upgrade','prior','proof_credits_monthly','2026-09',1,'COMMITTED');
CREATE TEMP TABLE upgrade_versions_before AS SELECT * FROM edition_versions;
CREATE TEMP TABLE upgrade_features_before AS SELECT * FROM edition_features;
CREATE TEMP TABLE upgrade_limits_before AS SELECT * FROM edition_limits;
CREATE TEMP TABLE upgrade_customer_before AS
 SELECT 'subscriptions' AS kind,to_jsonb(t) AS payload FROM subscriptions t
 UNION ALL SELECT 'entitlements',to_jsonb(t) FROM entitlements t
 UNION ALL SELECT 'quota_usage',to_jsonb(t) FROM quota_usage t
 UNION ALL SELECT 'quota_reservations',to_jsonb(t) FROM quota_reservations t
 UNION ALL SELECT 'entitlement_limits',to_jsonb(t) FROM entitlement_limits t;
