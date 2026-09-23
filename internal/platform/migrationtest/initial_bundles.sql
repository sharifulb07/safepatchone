-- Run after 000001, initial_bundles_before.sql, then 000002 in the same session.
BEGIN;
CREATE FUNCTION pg_temp.assert_seed(condition boolean, message text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF condition IS DISTINCT FROM true THEN RAISE EXCEPTION 'seed assertion failed: %', message; END IF;
END;
$$;
CREATE FUNCTION pg_temp.expect_seed_error(statement text, expected text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE actual text;
BEGIN
    BEGIN
        EXECUTE statement;
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS actual = RETURNED_SQLSTATE;
        IF actual <> expected THEN RAISE EXCEPTION 'expected SQLSTATE %, got %: %', expected, actual, SQLERRM; END IF;
        RETURN;
    END;
    RAISE EXCEPTION 'seed mutation unexpectedly succeeded';
END;
$$;

-- Independent acceptance data: incremental groups from the product contract.
CREATE TEMP TABLE expected_seed_deltas (position integer, edition_key text, expected_count integer, added text[]);
INSERT INTO expected_seed_deltas VALUES
(1, 'community', 4, ARRAY['verify.basic','verify.security','intent.basic','graph.local']),
(2, 'developer', 9, ARRAY['intent.advanced','proof.planner','proof.cloud_history','integration.github.private','language.additional_packs']),
(3, 'team', 16, ARRAY['platform.organizations','policy.central','graph.organization','proof.passport','proof.shared','audit.organization','verify.pr_enforcement']),
(4, 'business', 23, ARRAY['graph.cross_repo','testtrust','runtime.replay','runtime.behaviordna','business.invariants','incident.antibodies','verify.composition']),
(5, 'enterprise', 32, ARRAY['agent.mcp_gateway','agent.safelease','agent.temporalguard','proof.living','enterprise.sso','enterprise.audit','enterprise.self_hosted','enterprise.airgap','enterprise.lts']);

SELECT pg_temp.assert_seed((SELECT count(*)=5 FROM editions), 'five permanent editions');
SELECT pg_temp.assert_seed((SELECT count(*)=38 FROM feature_identities), '38 registered capability identities');
SELECT pg_temp.assert_seed((SELECT count(*)=5 FROM edition_versions WHERE edition_version_id IN (SELECT edition_key||'-2026-v1' FROM expected_seed_deltas)), 'five seeded versions');
SELECT pg_temp.assert_seed((SELECT count(*)=84 FROM edition_features WHERE edition_version_id IN (SELECT edition_key||'-2026-v1' FROM expected_seed_deltas)), '84 cumulative mappings');

DO $$
DECLARE offering record; expected text[]; actual text[];
BEGIN
    FOR offering IN SELECT * FROM expected_seed_deltas ORDER BY position LOOP
        SELECT array_agg(k ORDER BY k) INTO expected FROM expected_seed_deltas d
            CROSS JOIN LATERAL unnest(d.added) AS k WHERE d.position <= offering.position;
        SELECT array_agg(i.key ORDER BY i.key) INTO actual FROM edition_features f
            JOIN feature_identities i USING (feature_id)
            WHERE f.edition_version_id=offering.edition_key||'-2026-v1' AND f.enabled;
        PERFORM pg_temp.assert_seed(actual = expected AND cardinality(actual) = offering.expected_count, 'exact capability boundary for '||offering.edition_key);
        PERFORM pg_temp.assert_seed((SELECT sealed AND edition_key=offering.edition_key AND created_at='2026-09-06T00:00:00Z' FROM edition_versions WHERE edition_version_id=offering.edition_key||'-2026-v1'), 'sealed deterministic version');
    END LOOP;
END;
$$;

SELECT pg_temp.assert_seed(NOT EXISTS (SELECT 1 FROM edition_features WHERE edition_version_id IN (SELECT edition_key||'-2026-v1' FROM expected_seed_deltas) AND (NOT enabled OR limit_value IS NOT NULL OR configuration_json <> '{}'::jsonb)), 'no invented quotas or policy');
SELECT pg_temp.assert_seed(NOT EXISTS (SELECT 1 FROM edition_features f JOIN feature_identities i USING(feature_id) WHERE f.edition_version_id LIKE '%-2026-v1' AND i.key IN ('verify.api','verify.polyglot','intent.specforge','runtime.shadow','agent.provenance','agent.mcp_inventory')), 'unrequested capabilities remain unmapped');
SELECT pg_temp.assert_seed(NOT EXISTS(SELECT value FROM pre_seed_versions EXCEPT SELECT to_jsonb(v) FROM edition_versions v), 'pre-existing version metadata preserved');
SELECT pg_temp.assert_seed(NOT EXISTS(SELECT value FROM pre_seed_mappings EXCEPT SELECT to_jsonb(f) FROM edition_features f), 'pre-existing customer bundle preserved');

CREATE TEMP TABLE initial_bundle_history AS SELECT to_jsonb(f) AS value FROM edition_features f;
SELECT pg_temp.expect_seed_error($q$UPDATE edition_features SET enabled=false WHERE edition_version_id='team-2026-v1'$q$, '23514');
SELECT pg_temp.expect_seed_error($q$UPDATE edition_features SET limit_value=1 WHERE edition_version_id='developer-2026-v1'$q$, '23514');
SELECT pg_temp.expect_seed_error($q$DELETE FROM edition_features WHERE edition_version_id='community-2026-v1'$q$, '23514');
SELECT pg_temp.expect_seed_error($q$INSERT INTO edition_features VALUES ('community-2026-v1','feat_0025',true,NULL,'{}')$q$, '23514');
SELECT pg_temp.expect_seed_error($q$SELECT publish_edition_version('team-2026-v1','team',now(),'[]')$q$, '23505');

SELECT publish_edition_version('team-2027-v2','team','2027-01-01T00:00:00Z','[]');
SELECT pg_temp.assert_seed(NOT EXISTS(SELECT value FROM initial_bundle_history EXCEPT SELECT to_jsonb(f) FROM edition_features f), 'new bundle did not replace pinned mappings');
SELECT pg_temp.assert_seed(NOT EXISTS(SELECT to_jsonb(f) FROM edition_features f EXCEPT SELECT value FROM initial_bundle_history), 'new empty bundle did not alter prior data');
SELECT 'initial bundle assertions passed' AS result;
COMMIT;
