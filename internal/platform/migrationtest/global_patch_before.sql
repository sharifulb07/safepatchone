INSERT INTO organizations (organization_id,name,state,created_at)
VALUES ('org_global','GlobalPatch migration fixture','ACTIVE','2026-09-07T00:00:00Z');
INSERT INTO quota_accounts (organization_id) VALUES ('org_global');
INSERT INTO entitlement_limits (organization_id,limit_key,state,value)
VALUES ('org_global','repositories','INTEGER',3);
INSERT INTO quota_usage (organization_id,limit_key,period,reserved,committed)
VALUES ('org_global','repositories','lifetime',1,2);
INSERT INTO subscriptions (organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
VALUES ('org_global','sub_global','enterprise-2026-v3','manual','contract_global','ACTIVE',1,'2026-09-07T00:00:00Z','2026-09-07T00:00:00Z');
CREATE TEMP TABLE global_versions_before AS SELECT * FROM edition_versions;
CREATE TEMP TABLE global_features_before AS SELECT * FROM edition_features;
CREATE TEMP TABLE global_limits_before AS SELECT * FROM edition_limits;
CREATE TEMP TABLE global_customer_before AS
 SELECT 'subscriptions' AS kind,to_jsonb(t) AS payload FROM subscriptions t
 UNION ALL SELECT 'entitlements',to_jsonb(t) FROM entitlements t
 UNION ALL SELECT 'quota_usage',to_jsonb(t) FROM quota_usage t
 UNION ALL SELECT 'entitlement_limits',to_jsonb(t) FROM entitlement_limits t;
