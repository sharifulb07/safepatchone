-- Finite organization trials reuse the shared immutable entitlement ledger.
BEGIN;
CREATE TABLE organization_trials (
    organization_id text PRIMARY KEY REFERENCES organizations(organization_id) ON DELETE RESTRICT,
    trial_id text NOT NULL CHECK (trial_id ~ '^trial_[a-f0-9]{64}$'),
    edition_key text NOT NULL CHECK (edition_key IN ('developer','team','business')),
    edition_version_id text NOT NULL CHECK (split_part(edition_version_id,'-',1)=edition_key),
    bundle_sealed boolean NOT NULL DEFAULT true CHECK (bundle_sealed),
    starts_at timestamptz NOT NULL CHECK (starts_at >= '0001-01-01T00:00:00Z'),
    expires_at timestamptz NOT NULL CHECK (expires_at < '10000-01-01T00:00:00Z'),
    CHECK (expires_at>starts_at AND extract(epoch FROM expires_at-starts_at) BETWEEN 86400 AND 31536000),
    CHECK (mod(extract(epoch FROM expires_at-starts_at),86400)=0),
    CHECK (edition_key='business' OR extract(epoch FROM expires_at-starts_at)=1209600),
    UNIQUE (organization_id,trial_id),
    FOREIGN KEY (edition_version_id,bundle_sealed) REFERENCES edition_versions(edition_version_id,sealed) ON DELETE RESTRICT
);
-- Associations add tenant-bound foreign keys without changing legacy TRIAL
-- source references or rewriting any existing entitlement records.
CREATE TABLE organization_trial_grants (
    organization_id text NOT NULL,
    trial_id text NOT NULL,
    entitlement_id text COLLATE "C" NOT NULL,
    PRIMARY KEY (organization_id,entitlement_id),
    FOREIGN KEY (organization_id,trial_id) REFERENCES organization_trials(organization_id,trial_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id,entitlement_id) REFERENCES entitlements(organization_id,entitlement_id) ON DELETE RESTRICT
);
CREATE FUNCTION validate_organization_trial_grant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM organization_trials t
        JOIN entitlements e ON e.organization_id=t.organization_id AND e.source_reference=t.trial_id
        JOIN edition_features f ON f.edition_version_id=t.edition_version_id AND f.feature_id=e.feature_id
        WHERE t.organization_id=NEW.organization_id AND t.trial_id=NEW.trial_id
        AND e.entitlement_id=NEW.entitlement_id AND e.source='TRIAL'
        AND (e.canonical_record::jsonb->>'starts_at')::timestamptz=t.starts_at
        AND (e.canonical_record::jsonb->>'created_at')::timestamptz=t.starts_at
        AND (e.canonical_record::jsonb->>'expires_at')::timestamptz=t.expires_at
        AND (e.canonical_record::jsonb->>'enabled')::boolean=f.enabled
        AND (e.canonical_record::jsonb->>'limit_value')::bigint IS NOT DISTINCT FROM f.limit_value
    ) THEN
        RAISE EXCEPTION 'trial grant does not match organization offer' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER organization_trial_grant_validate BEFORE INSERT ON organization_trial_grants
    FOR EACH ROW EXECUTE FUNCTION validate_organization_trial_grant();
CREATE TRIGGER organization_trials_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON organization_trials
    FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER organization_trial_grants_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON organization_trial_grants
    FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
