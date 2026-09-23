INSERT INTO organizations VALUES ('agent_org','Agent','ACTIVE',now()),('agent_other','Other','ACTIVE',now());
INSERT INTO users VALUES ('agent_user','User',true,now());
INSERT INTO organization_members VALUES ('agent_org','agent_user','owner',true,now());
INSERT INTO projects VALUES ('agent_org','project','Project',true,now());
INSERT INTO repositories VALUES ('agent_org','project','repo','Repo',true,now());
CREATE TEMP TABLE agent_subscriptions_before AS SELECT * FROM subscriptions;
CREATE TEMP TABLE agent_entitlements_before AS SELECT * FROM entitlements;
