INSERT INTO organizations VALUES ('audit_org','Audit','ACTIVE',now()),('audit_other','Other','ACTIVE',now());
CREATE TEMP TABLE audit_subscriptions_before AS SELECT * FROM subscriptions;
CREATE TEMP TABLE audit_editions_before AS SELECT * FROM edition_limits;
