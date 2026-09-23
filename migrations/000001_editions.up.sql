BEGIN;

-- Minimal permanent feature references; full FeatureCatalog metadata remains
-- in the Go domain. Never recycle these IDs or derive grants from this table.
CREATE TABLE feature_identities (
    feature_id text PRIMARY KEY CHECK (feature_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    key text NOT NULL UNIQUE CHECK (octet_length(key) BETWEEN 1 AND 128)
);

INSERT INTO feature_identities (feature_id, key) VALUES
('feat_0001', 'verify.basic'), ('feat_0002', 'verify.security'),
('feat_0003', 'verify.api'), ('feat_0004', 'verify.polyglot'),
('feat_0005', 'intent.basic'), ('feat_0006', 'intent.advanced'), ('feat_0007', 'intent.specforge'),
('feat_0008', 'graph.local'), ('feat_0009', 'graph.organization'), ('feat_0010', 'graph.cross_repo'),
('feat_0011', 'proof.planner'), ('feat_0012', 'proof.passport'), ('feat_0013', 'proof.living'),
('feat_0014', 'testtrust'), ('feat_0015', 'runtime.replay'), ('feat_0016', 'runtime.behaviordna'),
('feat_0017', 'runtime.shadow'), ('feat_0018', 'business.invariants'), ('feat_0019', 'incident.antibodies'),
('feat_0020', 'agent.provenance'), ('feat_0021', 'agent.mcp_inventory'), ('feat_0022', 'agent.mcp_gateway'),
('feat_0023', 'agent.safelease'), ('feat_0024', 'agent.temporalguard'), ('feat_0025', 'enterprise.sso'),
('feat_0026', 'enterprise.audit'), ('feat_0027', 'enterprise.self_hosted'),
('feat_0028', 'enterprise.airgap'), ('feat_0029', 'enterprise.lts');

CREATE TABLE editions (
    key text PRIMARY KEY CHECK (key IN ('community', 'developer', 'team', 'business', 'enterprise')),
    name text NOT NULL CHECK (name = initcap(key))
);
INSERT INTO editions (key, name) VALUES
('community', 'Community'), ('developer', 'Developer'), ('team', 'Team'),
('business', 'Business'), ('enterprise', 'Enterprise');

CREATE TABLE edition_versions (
    edition_version_id text PRIMARY KEY,
    edition_key text NOT NULL REFERENCES editions(key) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL CHECK (isfinite(created_at) AND created_at >= '0001-01-01T00:00:00Z' AND created_at < '10000-01-01T00:00:00Z'),
    sealed boolean NOT NULL DEFAULT false,
    CHECK (octet_length(edition_version_id) <= 64
        AND edition_version_id ~ '^[a-z]+-[1-9][0-9]{3}-v[1-9][0-9]{0,8}$'
        AND split_part(edition_version_id, '-', 1) = edition_key)
);
CREATE INDEX edition_versions_by_edition ON edition_versions (edition_key, edition_version_id);

CREATE TABLE edition_features (
    edition_version_id text NOT NULL REFERENCES edition_versions(edition_version_id) ON DELETE RESTRICT,
    feature_id text NOT NULL REFERENCES feature_identities(feature_id) ON DELETE RESTRICT,
    enabled boolean NOT NULL,
    limit_value bigint CHECK (limit_value >= 0),
    configuration_json jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(configuration_json) = 'object' AND octet_length(configuration_json::text) <= 16384),
    PRIMARY KEY (edition_version_id, feature_id)
);

CREATE FUNCTION reject_edition_history_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'edition history is immutable' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER editions_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON editions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER feature_identities_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON feature_identities
    FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER edition_features_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON edition_features
    FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER edition_versions_no_removal BEFORE DELETE OR TRUNCATE ON edition_versions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();

CREATE FUNCTION guard_edition_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.sealed THEN
            RAISE EXCEPTION 'version must be assembled before sealing' USING ERRCODE = '23514';
        END IF;
    ELSE
        IF OLD.sealed OR NOT NEW.sealed
            OR NEW.edition_version_id IS DISTINCT FROM OLD.edition_version_id
            OR NEW.edition_key IS DISTINCT FROM OLD.edition_key
            OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
            RAISE EXCEPTION 'edition history is immutable' USING ERRCODE = '23514';
        END IF;
        IF (SELECT count(*) > 4096 OR coalesce(sum(octet_length(configuration_json::text)), 0) > 1048576
            FROM edition_features WHERE edition_version_id = OLD.edition_version_id) THEN
            RAISE EXCEPTION 'edition bundle exceeds resource limits' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER edition_version_guard BEFORE INSERT OR UPDATE ON edition_versions
    FOR EACH ROW EXECUTE FUNCTION guard_edition_version();

CREATE FUNCTION guard_edition_feature_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_sealed boolean;
BEGIN
    -- Lock the parent so insertion and sealing cannot race across transactions.
    SELECT sealed INTO parent_sealed FROM edition_versions
        WHERE edition_version_id = NEW.edition_version_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'unknown edition version' USING ERRCODE = '23503';
    END IF;
    IF parent_sealed THEN
        RAISE EXCEPTION 'cannot extend a sealed edition version' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER edition_feature_insert_guard BEFORE INSERT ON edition_features
    FOR EACH ROW EXECUTE FUNCTION guard_edition_feature_insert();

CREATE FUNCTION require_sealed_edition_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT (SELECT sealed FROM edition_versions WHERE edition_version_id = NEW.edition_version_id) THEN
        RAISE EXCEPTION 'edition version must be sealed in its creation transaction' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER edition_version_sealed_at_commit AFTER INSERT ON edition_versions
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_sealed_edition_version();

-- Invoker rights: this function never grants a caller elevated privileges.
-- Validate domain dependencies before calling; feature identities are the
-- persistence FK boundary, not the full dependency/entitlement service.
CREATE FUNCTION publish_edition_version(version_id text, edition_key text, registered_at timestamptz, mappings jsonb)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE item jsonb;
BEGIN
    IF mappings IS NULL OR jsonb_typeof(mappings) <> 'array' OR jsonb_array_length(mappings) > 4096 THEN
        RAISE EXCEPTION 'mappings must be a bounded JSON array' USING ERRCODE = '23514';
    END IF;
    INSERT INTO edition_versions (edition_version_id, edition_key, created_at)
        VALUES (version_id, edition_key, registered_at);
    FOR item IN SELECT value FROM jsonb_array_elements(mappings) LOOP
        IF jsonb_typeof(item) <> 'object'
            OR jsonb_typeof(item->'feature_id') IS DISTINCT FROM 'string'
            OR jsonb_typeof(item->'enabled') IS DISTINCT FROM 'boolean'
            OR item->>'edition_version_id' IS DISTINCT FROM version_id
            OR (item - ARRAY['edition_version_id', 'feature_id', 'enabled', 'limit_value', 'configuration_json']) <> '{}'::jsonb
            OR (item ? 'limit_value' AND item->'limit_value' <> 'null'::jsonb
                AND (jsonb_typeof(item->'limit_value') <> 'number' OR item->>'limit_value' !~ '^[0-9]+$')) THEN
            RAISE EXCEPTION 'invalid edition feature mapping' USING ERRCODE = '23514';
        END IF;
        INSERT INTO edition_features (edition_version_id, feature_id, enabled, limit_value, configuration_json)
        VALUES (version_id, item->>'feature_id', (item->>'enabled')::boolean,
            (item->>'limit_value')::bigint, coalesce(item->'configuration_json', '{}'::jsonb));
    END LOOP;
    UPDATE edition_versions SET sealed = true WHERE edition_version_id = version_id;
END;
$$;
REVOKE ALL ON FUNCTION publish_edition_version(text, text, timestamptz, jsonb) FROM PUBLIC;

-- Resolve table names in the installation schema, not a caller-controlled
-- search_path or automatically preferred temporary schema.
DO $install$
DECLARE installation_schema text := current_schema(); signature text;
BEGIN
    FOREACH signature IN ARRAY ARRAY[
        'reject_edition_history_change()', 'guard_edition_version()',
        'guard_edition_feature_insert()', 'require_sealed_edition_version()',
        'publish_edition_version(text,text,timestamptz,jsonb)'
    ] LOOP
        EXECUTE format('ALTER FUNCTION %I.%s SET search_path TO pg_catalog, %I, pg_temp',
            installation_schema, signature, installation_schema);
    END LOOP;
END;
$install$;

COMMIT;
