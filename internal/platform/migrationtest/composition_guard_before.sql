INSERT INTO organizations (organization_id,name,state,created_at)
VALUES ('org_composition','Composition migration fixture','ACTIVE','2026-09-07T00:00:00Z');
INSERT INTO quota_accounts (organization_id) VALUES ('org_composition');
INSERT INTO entitlement_limits (organization_id,limit_key,state,value)
VALUES ('org_composition','repositories','INTEGER',3);
INSERT INTO quota_usage (organization_id,limit_key,period,reserved,committed)
VALUES ('org_composition','repositories','lifetime',1,2);

CREATE TEMP TABLE composition_versions_before AS SELECT * FROM edition_versions;
CREATE TEMP TABLE composition_features_before AS SELECT * FROM edition_features;
CREATE TEMP TABLE composition_limits_before AS SELECT * FROM edition_limits;
CREATE TEMP TABLE composition_customer_before AS
 SELECT 'subscriptions' AS kind,to_jsonb(t) AS payload FROM subscriptions t
 UNION ALL SELECT 'entitlements',to_jsonb(t) FROM entitlements t
 UNION ALL SELECT 'quota_usage',to_jsonb(t) FROM quota_usage t
 UNION ALL SELECT 'entitlement_limits',to_jsonb(t) FROM entitlement_limits t;
