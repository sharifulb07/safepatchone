INSERT INTO organizations VALUES ('bill_a','A','ACTIVE','2026-09-07T12:00:00Z'),('bill_b','B','ACTIVE','2026-09-07T12:00:00Z');
INSERT INTO users VALUES ('billing_user','User',true,'2026-09-07T12:00:00Z');
INSERT INTO organization_members VALUES ('bill_a','billing_user','owner',true,'2026-09-07T12:00:00Z');
INSERT INTO projects VALUES ('bill_a','project','Project',true,'2026-09-07T12:00:00Z');
INSERT INTO repositories VALUES ('bill_a','project','repo','Repo',true,'2026-09-07T12:00:00Z');
INSERT INTO quota_accounts VALUES ('bill_a');
INSERT INTO quota_usage VALUES ('bill_a','proof_credits_monthly','2026-09',2,3);
INSERT INTO quota_reservations VALUES ('bill_a','existing_hold','proof_credits_monthly','2026-09',2,'RESERVED');
INSERT INTO subscriptions(organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
VALUES('bill_b','manual_contract','enterprise-2026-v1','manual','contract_external','ACTIVE',1,'2026-09-07T12:00:00Z','2026-09-07T12:00:00Z');
CREATE TEMP TABLE bill_usage_before AS SELECT * FROM quota_usage;
CREATE TEMP TABLE bill_reservations_before AS SELECT * FROM quota_reservations;
CREATE TEMP TABLE bill_repositories_before AS SELECT * FROM repositories;
CREATE TEMP TABLE bill_members_before AS SELECT * FROM organization_members;
CREATE TEMP TABLE bill_entitlements_before AS SELECT * FROM entitlements;
CREATE TEMP TABLE bill_features_before AS SELECT * FROM edition_features;
CREATE TEMP TABLE bill_limits_before AS SELECT * FROM edition_limits;
