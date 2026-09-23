INSERT INTO organizations VALUES ('github_a','A','ACTIVE',now()),('github_b','B','ACTIVE',now());
INSERT INTO users VALUES ('github_user','User',true,now());
INSERT INTO organization_members VALUES ('github_a','github_user','owner',true,now());
INSERT INTO projects VALUES ('github_a','project','Project',true,now());
INSERT INTO repositories VALUES ('github_a','project','gh_1','Repo',true,now());
INSERT INTO onboarding_github_connections VALUES ('github_a',7);
INSERT INTO onboarding_github_repositories VALUES ('github_a','gh_1',7,'owner/repo');
INSERT INTO subscriptions(organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
VALUES('github_a','paid','team-2026-v2','manual','contract','ACTIVE',1,now(),now());
CREATE TEMP TABLE github_subscription_before AS SELECT * FROM subscriptions;
