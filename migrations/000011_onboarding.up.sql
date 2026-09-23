BEGIN;
-- GitHub subject is the immutable identity; emails never link existing accounts.
CREATE TABLE login_identities (
    provider_subject text PRIMARY KEY CHECK (provider_subject ~ '^github:[1-9][0-9]{0,19}$'),
    user_id text NOT NULL UNIQUE REFERENCES users(user_id) ON DELETE RESTRICT,
    encrypted_token bytea NOT NULL CHECK (octet_length(encrypted_token) BETWEEN 28 AND 8192),
    token_expires_at timestamptz NOT NULL
);
CREATE TABLE login_sessions (
    token_hash text PRIMARY KEY CHECK (token_hash ~ '^[a-f0-9]{64}$'),
    user_id text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    csrf_hash text NOT NULL CHECK (csrf_hash ~ '^[a-f0-9]{64}$'),
    expires_at timestamptz NOT NULL
);
CREATE INDEX login_sessions_expiry ON login_sessions(expires_at);
CREATE TABLE login_attempts (
    state_hash text PRIMARY KEY CHECK (state_hash ~ '^[a-f0-9]{64}$'),
    browser_hash text NOT NULL CHECK (browser_hash ~ '^[a-f0-9]{64}$'),
    encrypted_verifier bytea NOT NULL CHECK (octet_length(encrypted_verifier) BETWEEN 28 AND 8192),
    expires_at timestamptz NOT NULL
);
CREATE INDEX login_attempts_expiry ON login_attempts(expires_at);
CREATE TABLE onboarding_states (
    organization_id text PRIMARY KEY REFERENCES organizations(organization_id) ON DELETE RESTRICT,
    creator_user_id text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    creation_attempt text NOT NULL CHECK (creation_attempt ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
    requested_edition_version_id text REFERENCES edition_versions(edition_version_id) ON DELETE RESTRICT,
    revision bigint NOT NULL CHECK (revision>0),
    canonical_state text NOT NULL CHECK (octet_length(canonical_state) BETWEEN 2 AND 32768),
    UNIQUE(creator_user_id,creation_attempt),
    CHECK ((canonical_state::jsonb->>'organization_id') IS NOT DISTINCT FROM organization_id),
    CHECK ((canonical_state::jsonb->>'revision')::bigint IS NOT DISTINCT FROM revision),
    CHECK (NULLIF(canonical_state::jsonb->>'edition_version_id','') IS NOT DISTINCT FROM requested_edition_version_id)
);
CREATE TABLE onboarding_github_connections (
    organization_id text PRIMARY KEY REFERENCES organizations(organization_id) ON DELETE RESTRICT,
    installation_id bigint NOT NULL UNIQUE CHECK (installation_id>0),
    UNIQUE(organization_id,installation_id)
);
CREATE TABLE onboarding_github_repositories (
    organization_id text NOT NULL,
    repository_id text NOT NULL,
    installation_id bigint NOT NULL,
    full_name text NOT NULL,
    PRIMARY KEY(organization_id,repository_id),
    FOREIGN KEY(organization_id,repository_id) REFERENCES repositories(organization_id,repository_id) ON DELETE RESTRICT,
    FOREIGN KEY(organization_id,installation_id) REFERENCES onboarding_github_connections(organization_id,installation_id) ON DELETE RESTRICT
);
-- This is a transactional outbox, not a second verification engine. Messages
-- carry IDs only; trusted workers reload state, permissions, quotas and source.
CREATE TABLE onboarding_proof_jobs (
    organization_id text NOT NULL REFERENCES organizations(organization_id) ON DELETE RESTRICT,
    proof_id text NOT NULL,
    actor_user_id text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    onboarding_revision bigint NOT NULL CHECK (onboarding_revision>0),
    canonical_request text NOT NULL CHECK (octet_length(canonical_request) BETWEEN 2 AND 32768),
    status text NOT NULL CHECK (status IN ('PENDING','QUEUED','RUNNING','PASS','FAIL','UNKNOWN','ERROR')),
    published_at timestamptz,
    lease_id text,
    lease_until timestamptz,
    evidence_reference text,
    PRIMARY KEY(organization_id,proof_id),
    UNIQUE(organization_id,onboarding_revision),
    CHECK ((canonical_request::jsonb->>'organization_id') IS NOT DISTINCT FROM organization_id),
    CHECK (status NOT IN ('PASS','FAIL') OR (evidence_reference IS NOT NULL AND length(evidence_reference) BETWEEN 1 AND 256))
);
CREATE FUNCTION guard_onboarding_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'onboarding history cannot be removed' USING ERRCODE='23514'; END IF;
 IF NEW.organization_id<>OLD.organization_id OR NEW.creator_user_id<>OLD.creator_user_id OR NEW.creation_attempt<>OLD.creation_attempt OR NEW.revision<>OLD.revision+1 THEN
  RAISE EXCEPTION 'invalid onboarding revision' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER onboarding_state_identity BEFORE UPDATE OR DELETE ON onboarding_states FOR EACH ROW EXECUTE FUNCTION guard_onboarding_state();
CREATE TRIGGER onboarding_state_no_truncate BEFORE TRUNCATE ON onboarding_states FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE FUNCTION guard_onboarding_proof() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'proof history cannot be removed' USING ERRCODE='23514'; END IF;
 IF NEW.organization_id<>OLD.organization_id OR NEW.proof_id<>OLD.proof_id OR NEW.actor_user_id<>OLD.actor_user_id OR NEW.onboarding_revision<>OLD.onboarding_revision OR NEW.canonical_request<>OLD.canonical_request
 OR (OLD.status IN ('PASS','FAIL','UNKNOWN','ERROR') AND (NEW.status<>OLD.status OR NEW.evidence_reference IS DISTINCT FROM OLD.evidence_reference)) THEN
  RAISE EXCEPTION 'proof identity or completed evidence is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER onboarding_proof_identity BEFORE UPDATE OR DELETE ON onboarding_proof_jobs FOR EACH ROW EXECUTE FUNCTION guard_onboarding_proof();
CREATE TRIGGER onboarding_proof_no_truncate BEFORE TRUNCATE ON onboarding_proof_jobs FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
