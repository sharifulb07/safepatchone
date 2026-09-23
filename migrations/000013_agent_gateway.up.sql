BEGIN;
CREATE TABLE agent_safe_leases (
 organization_id text NOT NULL,
 repository_id text NOT NULL,
 lease_id text NOT NULL CHECK(lease_id ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$'),
 actor_id text NOT NULL CHECK(actor_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 actor_kind text NOT NULL CHECK(actor_kind IN ('user','service_account')),
 actor_user_id text GENERATED ALWAYS AS (CASE WHEN actor_kind='user' THEN actor_id END) STORED,
 capabilities text NOT NULL CHECK(octet_length(capabilities) BETWEEN 2 AND 65536),
 issued_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 last_used_at timestamptz NOT NULL,
 max_calls integer NOT NULL CHECK(max_calls BETWEEN 1 AND 32),
 uses integer NOT NULL CHECK(uses BETWEEN 0 AND max_calls),
 revoked boolean NOT NULL DEFAULT false,
 PRIMARY KEY(organization_id,repository_id,lease_id),
 FOREIGN KEY(organization_id,repository_id) REFERENCES repositories(organization_id,repository_id) ON DELETE RESTRICT,
 FOREIGN KEY(organization_id,actor_user_id) REFERENCES organization_members(organization_id,user_id) ON DELETE RESTRICT,
 CHECK(issued_at >= '0001-01-01T00:00:00Z' AND expires_at < '10000-01-01T00:00:00Z'),
 CHECK(expires_at>issued_at AND expires_at<=issued_at+interval '600 seconds'),
 CHECK(last_used_at>=issued_at AND last_used_at<expires_at),
 CHECK(capabilities::jsonb->>'schema' IS NOT DISTINCT FROM 'safepatch.agent.capabilities.v1'),
 CHECK(capabilities::jsonb->'scope'->>'organization_id' IS NOT DISTINCT FROM organization_id),
 CHECK(capabilities::jsonb->'scope'->>'repository_id' IS NOT DISTINCT FROM repository_id),
 CHECK((capabilities::jsonb->>'call_budget')::integer IS NOT DISTINCT FROM max_calls),
 CHECK(expires_at IS NOT DISTINCT FROM issued_at + (capabilities::jsonb->>'lease_seconds')::integer * interval '1 second')
);
CREATE INDEX agent_safe_leases_active ON agent_safe_leases(organization_id,expires_at) WHERE NOT revoked;
CREATE TABLE agent_invocations (
 organization_id text NOT NULL,
 repository_id text NOT NULL,
 lease_id text NOT NULL,
 invocation_id text NOT NULL CHECK(invocation_id ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$'),
 call_digest text NOT NULL CHECK(call_digest ~ '^[a-f0-9]{64}$'),
 outcome text NOT NULL CHECK(outcome IN ('UNKNOWN','RETURNED','TOOL_ERROR')),
 result_digest text NOT NULL,
 finished boolean NOT NULL,
 PRIMARY KEY(organization_id,repository_id,lease_id,invocation_id),
 FOREIGN KEY(organization_id,repository_id,lease_id) REFERENCES agent_safe_leases(organization_id,repository_id,lease_id) ON DELETE RESTRICT,
 CHECK((NOT finished AND outcome='UNKNOWN' AND result_digest='') OR (finished AND outcome IN ('RETURNED','TOOL_ERROR') AND result_digest ~ '^[a-f0-9]{64}$'))
);
CREATE FUNCTION guard_agent_lease() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'lease history is immutable' USING ERRCODE='23514'; END IF;
 IF NEW.organization_id<>OLD.organization_id OR NEW.repository_id<>OLD.repository_id OR NEW.lease_id<>OLD.lease_id
 OR NEW.actor_id<>OLD.actor_id OR NEW.actor_kind<>OLD.actor_kind OR NEW.capabilities<>OLD.capabilities
 OR NEW.issued_at<>OLD.issued_at OR NEW.expires_at<>OLD.expires_at OR NEW.max_calls<>OLD.max_calls
 OR (OLD.revoked AND NOT NEW.revoked) OR NEW.last_used_at<OLD.last_used_at
 OR NEW.uses<OLD.uses OR NEW.uses>OLD.uses+1 OR (OLD.revoked AND NEW.uses<>OLD.uses)
 OR (NEW.uses=OLD.uses AND NEW.last_used_at<>OLD.last_used_at) THEN
 RAISE EXCEPTION 'lease identity or temporal authority changed' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION guard_agent_invocation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'invocation history is immutable' USING ERRCODE='23514'; END IF;
 IF OLD.finished OR NOT NEW.finished OR NEW.organization_id<>OLD.organization_id OR NEW.repository_id<>OLD.repository_id
 OR NEW.lease_id<>OLD.lease_id OR NEW.invocation_id<>OLD.invocation_id OR NEW.call_digest<>OLD.call_digest THEN
 RAISE EXCEPTION 'invocation cannot be replayed or rewritten' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER agent_leases_immutable BEFORE UPDATE OR DELETE ON agent_safe_leases FOR EACH ROW EXECUTE FUNCTION guard_agent_lease();
CREATE TRIGGER agent_invocations_immutable BEFORE UPDATE OR DELETE ON agent_invocations FOR EACH ROW EXECUTE FUNCTION guard_agent_invocation();
CREATE TRIGGER agent_leases_no_truncate BEFORE TRUNCATE ON agent_safe_leases FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER agent_invocations_no_truncate BEFORE TRUNCATE ON agent_invocations FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
