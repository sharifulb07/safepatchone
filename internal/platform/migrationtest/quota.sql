DO $$
BEGIN
    IF (SELECT count(*) FROM quota_limit_keys) <> 8 THEN RAISE EXCEPTION 'missing limits'; END IF;
    IF EXISTS (SELECT 1 FROM entitlement_limits) THEN RAISE EXCEPTION 'unexpected commercial defaults'; END IF;
    IF (SELECT count(*) FROM edition_versions) <> 5 THEN RAISE EXCEPTION 'edition history changed'; END IF;
END;
$$;
INSERT INTO quota_accounts VALUES ('org_a'), ('org_b');
INSERT INTO entitlement_limits VALUES ('org_a', 'repositories', 'INTEGER', 2);
INSERT INTO quota_usage VALUES ('org_a', 'repositories', 'lifetime', 1, 0);
INSERT INTO quota_reservations VALUES ('org_a', 'same_id', 'repositories', 'lifetime', 1, 'RESERVED');
INSERT INTO quota_usage VALUES ('org_b', 'repositories', 'lifetime', 1, 0);
INSERT INTO quota_reservations VALUES ('org_b', 'same_id', 'repositories', 'lifetime', 1, 'RESERVED');

-- Downgrade and disable policy preserve existing reservations and accounting.
UPDATE entitlement_limits SET value = 0, state = 'DISABLED' WHERE organization_id = 'org_a';
DO $$
BEGIN
    IF (SELECT reserved FROM quota_usage WHERE organization_id = 'org_a') <> 1
        OR (SELECT state FROM quota_reservations WHERE organization_id = 'org_a') <> 'RESERVED' THEN
        RAISE EXCEPTION 'downgrade corrupted accounting';
    END IF;
    BEGIN
        INSERT INTO entitlement_limits VALUES ('org_a', 'developers', 'UNKNOWN', 0);
        RAISE EXCEPTION 'unknown state accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO entitlement_limits VALUES ('org_a', 'developers', 'UNLIMITED', 1);
        RAISE EXCEPTION 'magic unlimited number accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO entitlement_limits VALUES ('org_a', 'developers', 'INTEGER', -1);
        RAISE EXCEPTION 'negative limit accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO entitlement_limits VALUES ('org_a', 'unknown', 'UNLIMITED', 0);
        RAISE EXCEPTION 'unknown key accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        INSERT INTO quota_usage VALUES ('org_a', 'retention_days', 'lifetime', 1, 0);
        RAISE EXCEPTION 'ceiling treated as consumption';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        UPDATE quota_usage SET reserved = 9223372036854775807, committed = 1 WHERE organization_id = 'org_a';
        RAISE EXCEPTION 'counter overflow accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO quota_reservations VALUES ('org_b', 'cross_tenant', 'developers', 'lifetime', 1, 'RESERVED');
        RAISE EXCEPTION 'missing tenant bucket accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL; END;
    BEGIN
        UPDATE quota_reservations SET organization_id = 'org_b' WHERE organization_id = 'org_a';
        RAISE EXCEPTION 'reservation reassigned';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        DELETE FROM quota_reservations WHERE organization_id = 'org_a';
        RAISE EXCEPTION 'idempotency history erased';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        INSERT INTO quota_usage VALUES ('org_a', 'proof_credits_monthly', '0000-01', 0, 0);
        RAISE EXCEPTION 'invalid monthly period';
    EXCEPTION WHEN check_violation THEN NULL; END;
END;
$$;

INSERT INTO quota_usage VALUES ('org_a', 'proof_credits_monthly', '2026-09', 0, 2);
INSERT INTO quota_reservations VALUES ('org_a', 'monthly', 'proof_credits_monthly', '2026-09', 2, 'COMMITTED');
DO $$
BEGIN
    BEGIN
        UPDATE quota_reservations SET state = 'RELEASED' WHERE organization_id = 'org_a' AND reservation_id = 'monthly';
        RAISE EXCEPTION 'monthly consumption refunded';
    EXCEPTION WHEN check_violation THEN NULL; END;
    BEGIN
        UPDATE quota_reservations SET amount = 3 WHERE organization_id = 'org_a' AND reservation_id = 'monthly';
        RAISE EXCEPTION 'reservation identity modified';
    EXCEPTION WHEN check_violation THEN NULL; END;
END;
$$;
-- Failed accounting transaction leaves both counters and lifecycle unchanged.
DO $$
BEGIN
    BEGIN
        UPDATE quota_usage SET reserved = 0, committed = 1 WHERE organization_id = 'org_a' AND limit_key = 'repositories';
        UPDATE quota_reservations SET state = 'COMMITTED' WHERE organization_id = 'org_a' AND reservation_id = 'same_id';
        RAISE EXCEPTION 'simulate worker failure';
    EXCEPTION WHEN raise_exception THEN NULL; END;
    IF (SELECT reserved FROM quota_usage WHERE organization_id = 'org_a' AND limit_key = 'repositories') <> 1
        OR (SELECT state FROM quota_reservations WHERE organization_id = 'org_a' AND reservation_id = 'same_id') <> 'RESERVED' THEN
        RAISE EXCEPTION 'transaction did not roll back';
    END IF;
END;
$$;
