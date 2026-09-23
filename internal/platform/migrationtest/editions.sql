-- Run only in a fresh disposable schema after 000001_editions.up.sql.
BEGIN;
CREATE FUNCTION pg_temp.assert_true(condition boolean, message text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF condition IS DISTINCT FROM true THEN RAISE EXCEPTION 'assertion failed: %', message; END IF;
END;
$$;
CREATE FUNCTION pg_temp.expect_sqlstate(statement text, expected text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE actual text;
BEGIN
    BEGIN
        EXECUTE statement;
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS actual = RETURNED_SQLSTATE;
        IF actual <> expected THEN RAISE EXCEPTION 'expected SQLSTATE %, got %: %', expected, actual, SQLERRM; END IF;
        RETURN;
    END;
    RAISE EXCEPTION 'statement unexpectedly succeeded: %', statement;
END;
$$;

SELECT pg_temp.assert_true((SELECT count(*) = 5 FROM editions), 'exactly five editions');
SELECT pg_temp.assert_true((SELECT count(*) = 29 FROM feature_identities), 'stable feature identities');
SELECT pg_temp.expect_sqlstate($q$INSERT INTO editions VALUES ('premium', 'Premium')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$UPDATE editions SET name = 'Other' WHERE key = 'team'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$DELETE FROM editions WHERE key = 'community'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$TRUNCATE editions CASCADE$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$UPDATE feature_identities SET key = 'changed' WHERE feature_id = 'feat_0001'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$DELETE FROM feature_identities$q$, '23514');

-- Every edition uses the same publication path. No default grants are seeded.
DO $$
DECLARE offering record; old_id text; new_id text;
BEGIN
    FOR offering IN SELECT key FROM editions LOOP
        old_id := offering.key || '-2026-v1';
        new_id := offering.key || '-2027-v2';
        PERFORM publish_edition_version(old_id, offering.key, '2026-09-06T00:00:00Z',
            jsonb_build_array(jsonb_build_object('edition_version_id', old_id, 'feature_id', 'feat_0001',
                'enabled', true, 'limit_value', 5, 'configuration_json', jsonb_build_object('policy', 'original'))));
        PERFORM publish_edition_version(new_id, offering.key, '2027-01-01T00:00:00Z',
            jsonb_build_array(jsonb_build_object('edition_version_id', new_id, 'feature_id', 'feat_0001',
                'enabled', false, 'limit_value', 0, 'configuration_json', jsonb_build_object('policy', 'revised'))));
    END LOOP;
END;
$$;
CREATE TEMP TABLE original_editions AS SELECT to_jsonb(v) AS value FROM edition_versions v;
CREATE TEMP TABLE original_mappings AS SELECT to_jsonb(f) AS value FROM edition_features f;
COMMIT;
BEGIN;
SELECT pg_temp.assert_true((SELECT count(*) = 10 AND bool_and(sealed) FROM edition_versions), 'all versions sealed');
SELECT pg_temp.assert_true((SELECT count(*) = 5 FROM edition_features WHERE edition_version_id LIKE '%-2026-v1' AND enabled AND limit_value = 5 AND configuration_json->>'policy' = 'original'), 'grandfathered bundles preserved');
SELECT pg_temp.assert_true((SELECT count(*) = 5 FROM edition_features WHERE edition_version_id LIKE '%-2027-v2' AND NOT enabled AND limit_value = 0), 'new versions independently configured');

SELECT pg_temp.expect_sqlstate($q$UPDATE edition_features SET enabled = false WHERE edition_version_id = 'team-2026-v1'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$UPDATE edition_features SET limit_value = 999 WHERE edition_version_id = 'team-2026-v1'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$UPDATE edition_features SET configuration_json = '{}' WHERE edition_version_id = 'team-2026-v1'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$DELETE FROM edition_features WHERE edition_version_id = 'team-2026-v1'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$TRUNCATE edition_features$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$INSERT INTO edition_features VALUES ('team-2026-v1', 'feat_0002', true, NULL, '{}')$q$, '23514');
CREATE TEMP TABLE edition_versions (edition_version_id text, sealed boolean);
INSERT INTO pg_temp.edition_versions VALUES ('team-2026-v1', false);
SELECT pg_temp.expect_sqlstate($q$INSERT INTO edition_features VALUES ('team-2026-v1', 'feat_0002', true, NULL, '{}')$q$, '23514');
DROP TABLE pg_temp.edition_versions;
SELECT pg_temp.expect_sqlstate($q$UPDATE edition_versions SET created_at = now() WHERE edition_version_id = 'team-2026-v1'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$UPDATE edition_versions SET sealed = false WHERE edition_version_id = 'team-2026-v1'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$DELETE FROM edition_versions WHERE edition_version_id = 'team-2026-v1'$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$TRUNCATE edition_versions CASCADE$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2026-v1', 'team', now(), '[]')$q$, '23505');
SELECT pg_temp.expect_sqlstate($q$INSERT INTO edition_versions VALUES ('team-2030-v1', 'team', now(), true)$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$DO $test$ BEGIN INSERT INTO edition_versions VALUES ('team-2030-v1', 'team', now(), false); SET CONSTRAINTS ALL IMMEDIATE; END $test$$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('business-2030-v1', 'team', now(), '[]')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('premium-2030-v1', 'premium', now(), '[]')$q$, '23503');

SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2030-v1","feature_id":"unknown","enabled":true}]')$q$, '23503');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2030-v1","feature_id":"feat_0001"}]')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2030-v1","feature_id":"feat_0001","enabled":"true"}]')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2030-v1","feature_id":"feat_0001","enabled":true,"limit_value":-1}]')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2030-v1","feature_id":"feat_0001","enabled":true,"limit_value":1.5}]')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2030-v1","feature_id":"feat_0001","enabled":true,"configuration_json":[]}]')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2030-v1","feature_id":"feat_0001","enabled":true,"configuration_json":null}]')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2026-v1","feature_id":"feat_0001","enabled":true}]')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2030-v1","feature_id":"feat_0001","enabled":true,"unknown_field":1}]')$q$, '23514');
SELECT pg_temp.expect_sqlstate($q$SELECT publish_edition_version('team-2030-v1','team',now(), '[{"edition_version_id":"team-2030-v1","feature_id":"feat_0001","enabled":true},{"edition_version_id":"team-2030-v1","feature_id":"feat_0001","enabled":false}]')$q$, '23505');
SELECT pg_temp.assert_true(NOT EXISTS(SELECT 1 FROM edition_versions WHERE edition_version_id LIKE '%-2030-v1'), 'failed publication rolled back');

SELECT pg_temp.assert_true(NOT EXISTS(SELECT to_jsonb(v) FROM edition_versions v EXCEPT SELECT value FROM original_editions), 'version history unchanged');
SELECT pg_temp.assert_true(NOT EXISTS(SELECT value FROM original_editions EXCEPT SELECT to_jsonb(v) FROM edition_versions v), 'no removed versions');
SELECT pg_temp.assert_true(NOT EXISTS(SELECT to_jsonb(f) FROM edition_features f EXCEPT SELECT value FROM original_mappings), 'mapping history unchanged');
SELECT pg_temp.assert_true(NOT EXISTS(SELECT value FROM original_mappings EXCEPT SELECT to_jsonb(f) FROM edition_features f), 'no removed mappings');

SELECT publish_edition_version('team-2031-v1','team',now(), '[{"edition_version_id":"team-2031-v1","feature_id":"feat_0001","enabled":false,"limit_value":null}]');
SELECT pg_temp.assert_true((SELECT limit_value IS NULL AND configuration_json = '{}'::jsonb FROM edition_features WHERE edition_version_id='team-2031-v1'), 'unspecified limit distinct from zero');
SELECT 'edition migration assertions passed' AS result;
COMMIT;
