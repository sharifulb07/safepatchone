BEGIN;
-- Baseline capture is shared; enterprise.audit controls export, never collection.
CREATE TABLE audit_events (
 organization_id text NOT NULL REFERENCES organizations(organization_id) ON DELETE RESTRICT,
 event_id text NOT NULL CHECK(event_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 record text NOT NULL CHECK(octet_length(record) BETWEEN 2 AND 8192),
 PRIMARY KEY(organization_id,event_id),
 CHECK(record::jsonb->>'organization_id' IS NOT DISTINCT FROM organization_id AND record::jsonb->>'id' IS NOT DISTINCT FROM event_id)
);
CREATE TRIGGER audit_events_immutable BEFORE UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER audit_events_no_truncate BEFORE TRUNCATE ON audit_events FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
