BEGIN;

CREATE TABLE users (
    user_id text PRIMARY KEY CHECK (user_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    display_name text NOT NULL CHECK (octet_length(display_name) BETWEEN 1 AND 256 AND btrim(display_name) <> ''),
    active boolean NOT NULL,
    created_at timestamptz NOT NULL CHECK (created_at >= '0001-01-01T00:00:00Z' AND created_at < '10000-01-01T00:00:00Z')
);
CREATE TABLE organizations (
    organization_id text PRIMARY KEY CHECK (organization_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 256 AND btrim(name) <> ''),
    state text NOT NULL CHECK (state IN ('PENDING','ACTIVE','SUSPENDED')),
    created_at timestamptz NOT NULL CHECK (created_at >= '0001-01-01T00:00:00Z' AND created_at < '10000-01-01T00:00:00Z')
);
-- Preserve existing accounting/evidence identities without inventing membership,
-- claiming a real organization, or enabling a subscription. Trusted provisioning
-- must reconcile and activate these placeholders before access can succeed.
INSERT INTO organizations (organization_id,name,state,created_at)
SELECT organization_id,'Unclaimed organization ' || organization_id,'PENDING',CURRENT_TIMESTAMP
FROM (SELECT organization_id FROM quota_accounts
      UNION SELECT tenant_id FROM evidence_graph_nodes
      UNION SELECT tenant_id FROM causal_change_nodes) legacy;
ALTER TABLE quota_accounts ADD CONSTRAINT quota_accounts_organization_fk
    FOREIGN KEY (organization_id) REFERENCES organizations(organization_id) ON DELETE RESTRICT;
ALTER TABLE evidence_graph_nodes ADD CONSTRAINT evidence_graph_organization_fk
    FOREIGN KEY (tenant_id) REFERENCES organizations(organization_id) ON DELETE RESTRICT;
ALTER TABLE causal_change_nodes ADD CONSTRAINT causal_change_organization_fk
    FOREIGN KEY (tenant_id) REFERENCES organizations(organization_id) ON DELETE RESTRICT;

CREATE TABLE organization_members (
    organization_id text NOT NULL REFERENCES organizations(organization_id) ON DELETE RESTRICT,
    user_id text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    role text NOT NULL CHECK (role IN ('owner','viewer')),
    active boolean NOT NULL,
    created_at timestamptz NOT NULL CHECK (created_at >= '0001-01-01T00:00:00Z' AND created_at < '10000-01-01T00:00:00Z'),
    PRIMARY KEY (organization_id,user_id)
);
CREATE INDEX organization_members_by_user ON organization_members (user_id,organization_id COLLATE "C");
CREATE TABLE projects (
    organization_id text NOT NULL REFERENCES organizations(organization_id) ON DELETE RESTRICT,
    project_id text NOT NULL CHECK (project_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$' AND project_id <> organization_id),
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 256 AND btrim(name) <> ''),
    active boolean NOT NULL,
    created_at timestamptz NOT NULL CHECK (created_at >= '0001-01-01T00:00:00Z' AND created_at < '10000-01-01T00:00:00Z'),
    PRIMARY KEY (organization_id,project_id)
);
CREATE TABLE repositories (
    organization_id text NOT NULL,
    project_id text NOT NULL,
    repository_id text NOT NULL CHECK (repository_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$' AND repository_id <> organization_id),
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 256 AND btrim(name) <> ''),
    active boolean NOT NULL,
    created_at timestamptz NOT NULL CHECK (created_at >= '0001-01-01T00:00:00Z' AND created_at < '10000-01-01T00:00:00Z'),
    PRIMARY KEY (organization_id,repository_id),
    FOREIGN KEY (organization_id,project_id) REFERENCES projects(organization_id,project_id) ON DELETE RESTRICT
);
CREATE INDEX repositories_by_project ON repositories (organization_id,project_id,repository_id);
-- Reference a sealed bundle without changing any prior edition version.
ALTER TABLE edition_versions ADD CONSTRAINT edition_version_seal_identity UNIQUE (edition_version_id,sealed);
CREATE TABLE subscriptions (
    organization_id text NOT NULL REFERENCES organizations(organization_id) ON DELETE RESTRICT,
    subscription_id text NOT NULL CHECK (subscription_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    edition_version_id text NOT NULL,
    bundle_sealed boolean NOT NULL DEFAULT true CHECK (bundle_sealed),
    provider text NOT NULL CHECK (provider ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    external_reference text NOT NULL CHECK (external_reference ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    state text NOT NULL CHECK (state IN ('PENDING','TRIALING','ACTIVE','PAST_DUE','CANCELED','EXPIRED')),
    revision bigint NOT NULL CHECK (revision > 0),
    created_at timestamptz NOT NULL CHECK (created_at >= '0001-01-01T00:00:00Z' AND created_at < '10000-01-01T00:00:00Z'),
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at AND updated_at < '10000-01-01T00:00:00Z'),
    PRIMARY KEY (organization_id,subscription_id),
    UNIQUE (provider,external_reference),
    FOREIGN KEY (edition_version_id,bundle_sealed) REFERENCES edition_versions(edition_version_id,sealed) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX subscriptions_one_current_per_organization ON subscriptions (organization_id)
    WHERE state IN ('PENDING','TRIALING','ACTIVE','PAST_DUE');

CREATE TABLE entitlements (
    organization_id text NOT NULL REFERENCES organizations(organization_id) ON DELETE RESTRICT,
    entitlement_id text COLLATE "C" NOT NULL CHECK (entitlement_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    feature_id text NOT NULL REFERENCES feature_identities(feature_id) ON DELETE RESTRICT,
    source text NOT NULL CHECK (source IN ('COMMUNITY_DEFAULT','SUBSCRIPTION','TRIAL','ENTERPRISE_LICENSE','PROMOTION','MANUAL_OVERRIDE')),
    source_reference text NOT NULL CHECK (octet_length(source_reference) BETWEEN 1 AND 256 AND source_reference ~ '^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,255}$'),
    subscription_id text GENERATED ALWAYS AS (CASE WHEN source='SUBSCRIPTION' THEN source_reference ELSE NULL END) STORED,
    canonical_record text NOT NULL CHECK (octet_length(canonical_record) BETWEEN 2 AND 32768),
    PRIMARY KEY (organization_id,entitlement_id),
    FOREIGN KEY (organization_id,subscription_id) REFERENCES subscriptions(organization_id,subscription_id) ON DELETE RESTRICT,
    CHECK (jsonb_typeof(canonical_record::jsonb) = 'object'),
    CHECK ((canonical_record::jsonb->>'organization_id') IS NOT DISTINCT FROM organization_id),
    CHECK ((canonical_record::jsonb->>'entitlement_id') IS NOT DISTINCT FROM entitlement_id),
    CHECK ((canonical_record::jsonb->>'feature_id') IS NOT DISTINCT FROM feature_id),
    CHECK ((canonical_record::jsonb->>'source') IS NOT DISTINCT FROM source),
    CHECK ((canonical_record::jsonb->>'source_reference') IS NOT DISTINCT FROM source_reference)
);
CREATE INDEX entitlements_by_feature ON entitlements (organization_id,feature_id,entitlement_id);
-- Usage remains the existing organization-scoped quota_usage ledger; no second
-- counter table or repository subscription is introduced.

CREATE FUNCTION guard_organization_entity_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'UPDATE' THEN
        RAISE EXCEPTION 'organization history cannot be removed' USING ERRCODE='23514';
    END IF;
    IF (to_jsonb(NEW)->'organization_id') IS DISTINCT FROM (to_jsonb(OLD)->'organization_id')
        OR (to_jsonb(NEW)->'user_id') IS DISTINCT FROM (to_jsonb(OLD)->'user_id')
        OR (to_jsonb(NEW)->'project_id') IS DISTINCT FROM (to_jsonb(OLD)->'project_id')
        OR (to_jsonb(NEW)->'repository_id') IS DISTINCT FROM (to_jsonb(OLD)->'repository_id')
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'entity ownership is immutable' USING ERRCODE='23514';
    END IF;
    IF TG_TABLE_NAME='subscriptions' AND (
        (to_jsonb(NEW)->'subscription_id') IS DISTINCT FROM (to_jsonb(OLD)->'subscription_id')
        OR (to_jsonb(NEW)->'provider') IS DISTINCT FROM (to_jsonb(OLD)->'provider')
        OR (to_jsonb(NEW)->'external_reference') IS DISTINCT FROM (to_jsonb(OLD)->'external_reference')
        OR (to_jsonb(NEW)->>'revision')::numeric <> (to_jsonb(OLD)->>'revision')::numeric+1
        OR (to_jsonb(NEW)->>'updated_at')::timestamptz < (to_jsonb(OLD)->>'updated_at')::timestamptz) THEN
        RAISE EXCEPTION 'invalid subscription revision' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;
DO $$ DECLARE entity text; BEGIN
    FOREACH entity IN ARRAY ARRAY['users','organizations','organization_members','projects','repositories','subscriptions'] LOOP
        EXECUTE format('CREATE TRIGGER %I BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION guard_organization_entity_identity()',entity||'_identity',entity);
        EXECUTE format('CREATE TRIGGER %I BEFORE TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION guard_organization_entity_identity()',entity||'_no_truncate',entity);
    END LOOP;
END; $$;
CREATE TRIGGER entitlements_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON entitlements
    FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
