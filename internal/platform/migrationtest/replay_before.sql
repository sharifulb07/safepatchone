INSERT INTO organizations VALUES ('replay_org','Replay','ACTIVE',now()),('replay_other','Other','ACTIVE',now());
INSERT INTO projects VALUES ('replay_org','project','Project',true,now());
INSERT INTO repositories VALUES ('replay_org','project','repo','Repo',true,now());
INSERT INTO quota_accounts VALUES ('replay_org');
INSERT INTO entitlement_limits VALUES ('replay_org','replay_requests_monthly','INTEGER',10);
CREATE TEMP TABLE replay_subscriptions_before AS SELECT * FROM subscriptions;
CREATE TEMP TABLE replay_limits_before AS SELECT * FROM edition_limits;
