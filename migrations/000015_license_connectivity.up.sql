BEGIN;
-- One organization-owned installation in the shared schema. License bytes are
-- still signature-verified by Go; these columns are trusted operator metadata.
CREATE TABLE license_installations (
 organization_id text PRIMARY KEY REFERENCES organizations(organization_id) ON DELETE RESTRICT,
 revision bigint NOT NULL CHECK(revision > 0),
 revoked boolean NOT NULL DEFAULT false,
 envelope bytea NOT NULL CHECK(octet_length(envelope) BETWEEN 1 AND 262144),
 confirmed_at timestamptz CHECK(confirmed_at >= '0001-01-01T00:00:00Z' AND confirmed_at < '10000-01-01T00:00:00Z'),
 refresh_denied boolean NOT NULL DEFAULT false
);
CREATE FUNCTION guard_license_installation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.organization_id <> OLD.organization_id OR NEW.revision <> OLD.revision + 1
 OR (OLD.confirmed_at IS NOT NULL AND (NEW.confirmed_at IS NULL OR NEW.confirmed_at < OLD.confirmed_at)) THEN
 RAISE EXCEPTION 'license identity, revision or confirmation regressed' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER license_installation_revision BEFORE UPDATE ON license_installations FOR EACH ROW EXECUTE FUNCTION guard_license_installation();
CREATE TRIGGER license_installations_no_delete BEFORE DELETE ON license_installations FOR EACH ROW EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER license_installations_no_truncate BEFORE TRUNCATE ON license_installations FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
