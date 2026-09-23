CREATE TEMP TABLE graph_editions_before AS SELECT * FROM edition_versions;
CREATE TEMP TABLE graph_grants_before AS SELECT * FROM edition_features;
INSERT INTO quota_accounts VALUES ('org');
INSERT INTO entitlement_limits VALUES ('org','retention_days','UNLIMITED',0);
INSERT INTO quota_usage VALUES ('org','repositories','lifetime',0,1);
INSERT INTO quota_reservations VALUES ('org','existing_repo','repositories','lifetime',1,'COMMITTED');
