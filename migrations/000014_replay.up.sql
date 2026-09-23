BEGIN;
INSERT INTO quota_limit_keys(key,accounting) VALUES
('dataset_retention_days', 'CEILING'), ('replay_concurrency', 'CAPACITY');
-- Extend the one accounting ledger without renaming or rewriting prior keys.
DO $$ DECLARE constraint_name text; BEGIN
 FOR constraint_name IN SELECT conname FROM pg_constraint WHERE conrelid='quota_usage'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%limit_key%' LOOP
  EXECUTE format('ALTER TABLE quota_usage DROP CONSTRAINT %I',constraint_name);
 END LOOP;
END $$;
ALTER TABLE quota_usage ADD CONSTRAINT quota_usage_resource_period CHECK (
 (limit_key IN ('proof_credits_monthly','replay_requests_monthly','fuzz_seconds_monthly') AND period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$' AND left(period,4)<>'0000')
 OR (limit_key IN ('repositories','developers','verification_concurrency','artifact_storage_bytes','replay_concurrency') AND period='lifetime')
);
CREATE TABLE replay_datasets (
 organization_id text NOT NULL,
 repository_id text NOT NULL,
 dataset_id text NOT NULL CHECK(dataset_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 digest text NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'),
 bytes bigint NOT NULL CHECK(bytes BETWEEN 2 AND 4194304),
 redaction_digest text NOT NULL CHECK(redaction_digest ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(organization_id,repository_id,dataset_id),
 UNIQUE(organization_id,repository_id,digest),
 FOREIGN KEY(organization_id,repository_id) REFERENCES repositories(organization_id,repository_id) ON DELETE RESTRICT,
 CHECK(created_at>='0001-01-01T00:00:00Z' AND expires_at<'10000-01-01T00:00:00Z' AND expires_at>created_at AND expires_at<=created_at+interval '366 days')
);
CREATE INDEX replay_dataset_expiry ON replay_datasets(expires_at);
CREATE TABLE replay_runs (
 organization_id text NOT NULL,
 repository_id text NOT NULL,
 run_id text NOT NULL CHECK(run_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,99}$'),
 dataset_digest text NOT NULL,
 decision text NOT NULL CHECK(decision IN ('UNKNOWN','MATCH','DIFFERENT')),
 result text CHECK(octet_length(result) BETWEEN 2 AND 65536),
 PRIMARY KEY(organization_id,repository_id,run_id),
 FOREIGN KEY(organization_id,repository_id,dataset_digest) REFERENCES replay_datasets(organization_id,repository_id,digest) ON DELETE RESTRICT,
 CHECK(result IS NOT NULL OR decision='UNKNOWN'),
 CHECK(result IS NULL OR (result::jsonb->>'dataset_digest' IS NOT DISTINCT FROM dataset_digest AND result::jsonb->>'decision' IS NOT DISTINCT FROM decision))
);
CREATE TRIGGER replay_datasets_immutable BEFORE UPDATE OR DELETE ON replay_datasets FOR EACH ROW EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER replay_datasets_no_truncate BEFORE TRUNCATE ON replay_datasets FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE FUNCTION guard_replay_run() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'replay history cannot be removed' USING ERRCODE='23514'; END IF;
 IF OLD.result IS NOT NULL OR NEW.result IS NULL OR NEW.organization_id<>OLD.organization_id OR NEW.repository_id<>OLD.repository_id OR NEW.run_id<>OLD.run_id OR NEW.dataset_digest<>OLD.dataset_digest THEN
 RAISE EXCEPTION 'replay result or identity is immutable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER replay_runs_immutable BEFORE UPDATE OR DELETE ON replay_runs FOR EACH ROW EXECUTE FUNCTION guard_replay_run();
CREATE TRIGGER replay_runs_no_truncate BEFORE TRUNCATE ON replay_runs FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
