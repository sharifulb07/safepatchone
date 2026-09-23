BEGIN;

-- Resource identities are separate from feature identities. No commercial
-- numeric defaults are inferred; absent organization policies deny admission.
CREATE TABLE quota_limit_keys (
    key text PRIMARY KEY,
    accounting text NOT NULL CHECK (accounting IN ('CAPACITY', 'MONTHLY', 'CEILING'))
);
INSERT INTO quota_limit_keys (key, accounting) VALUES
('repositories', 'CAPACITY'), ('developers', 'CAPACITY'),
('proof_credits_monthly', 'MONTHLY'), ('verification_concurrency', 'CAPACITY'),
('retention_days', 'CEILING'), ('artifact_storage_bytes', 'CAPACITY'),
('replay_requests_monthly', 'MONTHLY'), ('fuzz_seconds_monthly', 'MONTHLY');
CREATE TRIGGER quota_keys_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON quota_limit_keys
    FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();

-- Organization registry/membership does not yet exist in this schema. This is
-- an accounting lock identity, never evidence of organization membership.
CREATE TABLE quota_accounts (
    organization_id text PRIMARY KEY CHECK (organization_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$')
);
CREATE TABLE entitlement_limits (
    organization_id text NOT NULL REFERENCES quota_accounts(organization_id) ON DELETE RESTRICT,
    limit_key text NOT NULL REFERENCES quota_limit_keys(key) ON DELETE RESTRICT,
    state text NOT NULL CHECK (state IN ('UNLIMITED', 'INTEGER', 'DISABLED')),
    value bigint NOT NULL CHECK (value >= 0 AND (state = 'INTEGER' OR value = 0)),
    PRIMARY KEY (organization_id, limit_key)
);
CREATE TABLE quota_usage (
    organization_id text NOT NULL REFERENCES quota_accounts(organization_id) ON DELETE RESTRICT,
    limit_key text NOT NULL REFERENCES quota_limit_keys(key) ON DELETE RESTRICT,
    period text NOT NULL,
    reserved bigint NOT NULL CHECK (reserved >= 0),
    committed bigint NOT NULL CHECK (committed >= 0 AND reserved <= 9223372036854775807 - committed),
    PRIMARY KEY (organization_id, limit_key, period),
    CHECK ((limit_key IN ('proof_credits_monthly', 'replay_requests_monthly', 'fuzz_seconds_monthly')
            AND period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$' AND left(period, 4) <> '0000')
        OR (limit_key IN ('repositories', 'developers', 'verification_concurrency', 'artifact_storage_bytes') AND period = 'lifetime'))
);
CREATE TABLE quota_reservations (
    organization_id text NOT NULL,
    reservation_id text NOT NULL CHECK (reservation_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    limit_key text NOT NULL,
    period text NOT NULL,
    amount bigint NOT NULL CHECK (amount > 0),
    state text NOT NULL CHECK (state IN ('RESERVED', 'COMMITTED', 'RELEASED')),
    PRIMARY KEY (organization_id, reservation_id),
    FOREIGN KEY (organization_id, limit_key, period)
        REFERENCES quota_usage(organization_id, limit_key, period) ON DELETE RESTRICT
);
CREATE INDEX quota_reservations_by_bucket ON quota_reservations (organization_id, limit_key, period, state);

-- Preserve idempotency identities and prevent refunds of consumed monthly work.
CREATE FUNCTION guard_quota_reservation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'UPDATE' THEN
        RAISE EXCEPTION 'reservation history cannot be removed' USING ERRCODE = '23514';
    END IF;
    IF ROW(NEW.organization_id, NEW.reservation_id, NEW.limit_key, NEW.period, NEW.amount)
        IS DISTINCT FROM ROW(OLD.organization_id, OLD.reservation_id, OLD.limit_key, OLD.period, OLD.amount)
        OR (OLD.state = 'RELEASED' AND NEW.state <> OLD.state)
        OR (OLD.state = 'COMMITTED' AND NEW.state <> OLD.state
            AND (NEW.state <> 'RELEASED' OR OLD.limit_key IN ('proof_credits_monthly', 'replay_requests_monthly', 'fuzz_seconds_monthly'))) THEN
        RAISE EXCEPTION 'invalid reservation transition' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER quota_reservation_guard BEFORE UPDATE OR DELETE ON quota_reservations
    FOR EACH ROW EXECUTE FUNCTION guard_quota_reservation();
CREATE TRIGGER quota_reservations_no_truncate BEFORE TRUNCATE ON quota_reservations
    FOR EACH STATEMENT EXECUTE FUNCTION guard_quota_reservation();

COMMIT;
