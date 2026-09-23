BEGIN;
-- Operators bind automation to a real organization member. Webhook sender IDs
-- never create users, memberships, entitlements, policy or automation authority.
CREATE TABLE github_pr_bindings (
 organization_id text NOT NULL,
 repository_id text NOT NULL,
 actor_user_id text NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 policy_hash text NOT NULL CHECK(policy_hash ~ '^[a-f0-9]{64}$'),
 PRIMARY KEY(organization_id,repository_id),
 FOREIGN KEY(organization_id,repository_id) REFERENCES onboarding_github_repositories(organization_id,repository_id) ON DELETE RESTRICT,
 FOREIGN KEY(organization_id,actor_user_id) REFERENCES organization_members(organization_id,user_id) ON DELETE RESTRICT
);
CREATE TABLE github_pr_jobs (
 job_id text PRIMARY KEY CHECK(job_id ~ '^pr_[a-f0-9]{64}$'),
 organization_id text NOT NULL,
 repository_id text NOT NULL,
 canonical_job text NOT NULL CHECK(octet_length(canonical_job) BETWEEN 2 AND 8192),
 status text NOT NULL CHECK(status IN ('PENDING','RUNNING','READY','DONE')),
 published_at timestamptz,
 lease_id text,
 lease_until timestamptz,
 canonical_result text CHECK(octet_length(canonical_result) BETWEEN 2 AND 8192),
 FOREIGN KEY(organization_id,repository_id) REFERENCES github_pr_bindings(organization_id,repository_id) ON DELETE RESTRICT,
 CHECK(canonical_job::jsonb->>'id' IS NOT DISTINCT FROM job_id),
 CHECK(canonical_job::jsonb->'binding'->>'OrganizationID' IS NOT DISTINCT FROM organization_id),
 CHECK(canonical_job::jsonb->'binding'->>'RepositoryID' IS NOT DISTINCT FROM repository_id),
 CHECK(status NOT IN ('READY','DONE') OR canonical_result IS NOT NULL)
);
CREATE TABLE github_pr_deliveries (
 delivery_id text PRIMARY KEY CHECK(delivery_id ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$'),
 payload_hash text NOT NULL CHECK(payload_hash ~ '^[a-f0-9]{64}$'),
 job_id text NOT NULL REFERENCES github_pr_jobs(job_id) ON DELETE RESTRICT,
 received_at timestamptz NOT NULL
);
CREATE INDEX github_pr_pending ON github_pr_jobs(organization_id,job_id) WHERE status<>'DONE';
CREATE INDEX github_pr_receipts_window ON github_pr_deliveries(received_at,job_id);
CREATE TRIGGER github_pr_deliveries_immutable BEFORE UPDATE OR DELETE ON github_pr_deliveries FOR EACH ROW EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER github_pr_deliveries_no_truncate BEFORE TRUNCATE ON github_pr_deliveries FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE FUNCTION guard_github_pr_job() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'PR job history cannot be removed' USING ERRCODE='23514'; END IF;
 IF NEW.job_id<>OLD.job_id OR NEW.organization_id<>OLD.organization_id OR NEW.repository_id<>OLD.repository_id OR NEW.canonical_job<>OLD.canonical_job
 OR (OLD.canonical_result IS NOT NULL AND NEW.canonical_result IS DISTINCT FROM OLD.canonical_result)
 OR (OLD.status='DONE' AND NEW.status<>'DONE')
 OR (OLD.status='READY' AND NEW.status NOT IN ('READY','DONE'))
 OR (OLD.status='RUNNING' AND NEW.status NOT IN ('RUNNING','READY'))
 OR (OLD.status='PENDING' AND NEW.status NOT IN ('PENDING','RUNNING')) THEN
  RAISE EXCEPTION 'PR identity and completed evidence are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER github_pr_job_immutable BEFORE UPDATE OR DELETE ON github_pr_jobs FOR EACH ROW EXECUTE FUNCTION guard_github_pr_job();
CREATE TRIGGER github_pr_jobs_no_truncate BEFORE TRUNCATE ON github_pr_jobs FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
