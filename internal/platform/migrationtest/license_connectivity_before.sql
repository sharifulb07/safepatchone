INSERT INTO organizations VALUES ('license_org','License','ACTIVE',now()),('license_other','Other','ACTIVE',now());
CREATE TEMP TABLE license_subscriptions_before AS SELECT * FROM subscriptions;
CREATE TEMP TABLE license_limits_before AS SELECT * FROM edition_limits;
CREATE TEMP TABLE license_usage_before AS SELECT * FROM quota_usage;
