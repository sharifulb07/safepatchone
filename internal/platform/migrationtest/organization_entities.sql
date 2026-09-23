DO $$
BEGIN
    IF (SELECT count(*) FROM organizations WHERE organization_id IN ('legacy','evidence_only') AND state='PENDING') <> 2
        OR EXISTS (SELECT 1 FROM organization_members) OR EXISTS (SELECT 1 FROM subscriptions) THEN
        RAISE EXCEPTION 'legacy identity migration granted access';
    END IF;
    IF EXISTS ((SELECT * FROM saas_usage_before EXCEPT SELECT * FROM quota_usage)
        UNION ALL (SELECT * FROM quota_usage EXCEPT SELECT * FROM saas_usage_before))
        OR EXISTS ((SELECT * FROM saas_holds_before EXCEPT SELECT * FROM quota_reservations)
        UNION ALL (SELECT * FROM quota_reservations EXCEPT SELECT * FROM saas_holds_before))
        OR EXISTS ((SELECT * FROM saas_evidence_before EXCEPT SELECT * FROM evidence_graph_nodes)
        UNION ALL (SELECT * FROM evidence_graph_nodes EXCEPT SELECT * FROM saas_evidence_before))
        OR EXISTS ((SELECT * FROM saas_editions_before EXCEPT SELECT * FROM edition_versions)
        UNION ALL (SELECT * FROM edition_versions EXCEPT SELECT * FROM saas_editions_before))
        OR EXISTS ((SELECT * FROM saas_grants_before EXCEPT SELECT * FROM edition_features)
        UNION ALL (SELECT * FROM edition_features EXCEPT SELECT * FROM saas_grants_before)) THEN
        RAISE EXCEPTION 'migration changed existing history';
    END IF;
END; $$;
INSERT INTO users VALUES ('user','User',true,'2026-09-06T12:00:00Z');
INSERT INTO organizations VALUES ('org_a','A','ACTIVE','2026-09-06T12:00:00Z'),('org_b','B','ACTIVE','2026-09-06T12:00:00Z');
INSERT INTO organization_members VALUES ('org_a','user','owner',true,'2026-09-06T12:00:00Z'),('org_b','user','viewer',true,'2026-09-06T12:00:00Z');
INSERT INTO projects VALUES ('org_a','project','Project A',true,'2026-09-06T12:00:00Z'),('org_b','project','Project B',true,'2026-09-06T12:00:00Z');
INSERT INTO projects VALUES ('org_a','only_a','Private project',true,'2026-09-06T12:00:00Z');
INSERT INTO repositories VALUES ('org_a','project','repo','Repo A',true,'2026-09-06T12:00:00Z'),('org_b','project','repo','Repo B',true,'2026-09-06T12:00:00Z');
INSERT INTO subscriptions (organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at) VALUES
    ('org_a','sub_a','team-2026-v1','manual','contract_a','ACTIVE',1,'2026-09-06T12:00:00Z','2026-09-06T12:00:00Z'),
    ('org_b','sub_b','enterprise-2026-v1','manual','contract_b','ACTIVE',1,'2026-09-06T12:00:00Z','2026-09-06T12:00:00Z');
CREATE FUNCTION pg_temp.saas_grant(ten text,id text,sub text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO entitlements (organization_id,entitlement_id,feature_id,source,source_reference,canonical_record)
    VALUES (ten,id,'feat_0011','SUBSCRIPTION',sub,json_build_object('organization_id',ten,'entitlement_id',id,'feature_id','feat_0011',
        'source','SUBSCRIPTION','source_reference',sub,'enabled',true,'limit_value',NULL,'starts_at','2026-09-06T12:00:00Z',
        'expires_at',NULL,'metadata',json_build_object(),'created_at','2026-09-06T12:00:00Z')::text);
END; $$;
SELECT pg_temp.saas_grant('org_a','grant','sub_a');
SELECT pg_temp.saas_grant('org_b','grant','sub_b');
DO $$
DECLARE statement text;
BEGIN
    IF (SELECT count(*) FROM organization_members WHERE user_id='user') <> 2 THEN
        RAISE EXCEPTION 'multi-organization membership rejected';
    END IF;
    BEGIN
        INSERT INTO quota_accounts VALUES ('unknown');
        RAISE EXCEPTION 'usage account without organization accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        INSERT INTO organization_members VALUES ('org_a','missing','owner',true,'2026-09-06T12:00:00Z');
        RAISE EXCEPTION 'membership without user accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        INSERT INTO repositories VALUES ('org_b','only_a','foreign','Foreign',true,'2026-09-06T12:00:00Z');
        RAISE EXCEPTION 'foreign project accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        PERFORM pg_temp.saas_grant('org_a','foreign_grant','sub_b');
        RAISE EXCEPTION 'foreign subscription grant accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        INSERT INTO subscriptions (organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
        VALUES ('org_a','second','business-2026-v1','manual','second','TRIALING',1,'2026-09-06T12:00:00Z','2026-09-06T12:00:00Z');
        RAISE EXCEPTION 'second current subscription accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    FOREACH statement IN ARRAY ARRAY[
        'UPDATE repositories SET organization_id=''org_b'' WHERE organization_id=''org_a''',
        'UPDATE subscriptions SET organization_id=''org_b'' WHERE organization_id=''org_a''',
        'UPDATE subscriptions SET revision=1 WHERE organization_id=''org_a''',
        'UPDATE organization_members SET user_id=''other'' WHERE organization_id=''org_a''',
        'UPDATE entitlements SET source_reference=''sub_b'' WHERE organization_id=''org_a''',
        'DELETE FROM entitlements', 'DELETE FROM subscriptions', 'TRUNCATE organizations CASCADE'] LOOP
        BEGIN EXECUTE statement; RAISE EXCEPTION 'ownership/history mutation accepted';
        EXCEPTION WHEN check_violation THEN NULL; END;
    END LOOP;
END; $$;
CREATE TEMP TABLE saas_entities_before AS SELECT * FROM repositories;
CREATE TEMP TABLE saas_entitlements_before AS SELECT * FROM entitlements;
UPDATE subscriptions SET edition_version_id='developer-2026-v1',revision=2 WHERE organization_id='org_a' AND subscription_id='sub_a' AND revision=1;
UPDATE subscriptions SET edition_version_id='team-2026-v1',revision=3 WHERE organization_id='org_a' AND subscription_id='sub_a' AND revision=2;
UPDATE subscriptions SET state='CANCELED',revision=4 WHERE organization_id='org_a' AND subscription_id='sub_a' AND revision=3;
INSERT INTO subscriptions (organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at)
VALUES ('org_a','replacement','team-2026-v1','manual','replacement','ACTIVE',1,'2026-09-06T12:00:00Z','2026-09-06T12:00:00Z');
DO $$
BEGIN
    IF EXISTS ((SELECT * FROM saas_entities_before EXCEPT SELECT * FROM repositories)
        UNION ALL (SELECT * FROM repositories EXCEPT SELECT * FROM saas_entities_before))
        OR EXISTS ((SELECT * FROM saas_entitlements_before EXCEPT SELECT * FROM entitlements)
        UNION ALL (SELECT * FROM entitlements EXCEPT SELECT * FROM saas_entitlements_before))
        OR EXISTS ((SELECT * FROM saas_usage_before EXCEPT SELECT * FROM quota_usage)
        UNION ALL (SELECT * FROM quota_usage EXCEPT SELECT * FROM saas_usage_before)) THEN
        RAISE EXCEPTION 'subscription plan change corrupted organization data';
    END IF;
    IF (SELECT role FROM organization_members WHERE organization_id='org_b' AND user_id='user') <> 'viewer' THEN
        RAISE EXCEPTION 'subscription changed actor permissions';
    END IF;
END; $$;
