BEGIN;
CREATE TABLE billing_connections (
 connection_id text PRIMARY KEY CHECK (connection_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 provider text NOT NULL CHECK (provider ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 account_reference text NOT NULL CHECK (account_reference ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 live_mode boolean NOT NULL,
 UNIQUE(provider,account_reference,live_mode)
);
CREATE TABLE billing_customers (
 connection_id text NOT NULL REFERENCES billing_connections(connection_id) ON DELETE RESTRICT,
 organization_id text NOT NULL UNIQUE REFERENCES organizations(organization_id) ON DELETE RESTRICT,
 customer_reference text NOT NULL CHECK (customer_reference ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 PRIMARY KEY(connection_id,organization_id), UNIQUE(connection_id,customer_reference)
);
CREATE TABLE billing_events (
 connection_id text NOT NULL,
 event_id text COLLATE "C" NOT NULL CHECK (event_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 organization_id text NOT NULL,
 external_reference text NOT NULL CHECK (external_reference ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 occurred_at timestamptz NOT NULL CHECK (isfinite(occurred_at)),
 received_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 applied_at timestamptz CHECK (isfinite(applied_at)),
 PRIMARY KEY(connection_id,event_id),
 FOREIGN KEY(connection_id,organization_id) REFERENCES billing_customers(connection_id,organization_id) ON DELETE RESTRICT,
 UNIQUE(connection_id,event_id,organization_id)
);
CREATE INDEX billing_events_pending ON billing_events(connection_id,event_id) WHERE applied_at IS NULL;
CREATE TABLE billing_subscription_state (
 organization_id text NOT NULL,
 subscription_id text NOT NULL,
 connection_id text NOT NULL,
 revision bigint NOT NULL CHECK (revision>0),
 access_until timestamptz CHECK (isfinite(access_until)),
 payment_settled boolean NOT NULL,
 cancel_at_period_end boolean NOT NULL,
 PRIMARY KEY(organization_id,subscription_id),
 FOREIGN KEY(organization_id,subscription_id) REFERENCES subscriptions(organization_id,subscription_id) ON DELETE RESTRICT,
 FOREIGN KEY(connection_id,organization_id) REFERENCES billing_customers(connection_id,organization_id) ON DELETE RESTRICT
);
CREATE TABLE billing_subscription_history (
 organization_id text NOT NULL,
 subscription_id text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 connection_id text NOT NULL,
 event_id text NOT NULL,
 canonical_snapshot text NOT NULL CHECK(octet_length(canonical_snapshot) BETWEEN 2 AND 32768 AND jsonb_typeof(canonical_snapshot::jsonb)='object'),
 PRIMARY KEY(organization_id,subscription_id,revision),
 FOREIGN KEY(organization_id,subscription_id) REFERENCES subscriptions(organization_id,subscription_id) ON DELETE RESTRICT,
 FOREIGN KEY(connection_id,event_id,organization_id) REFERENCES billing_events(connection_id,event_id,organization_id) ON DELETE RESTRICT
);
CREATE FUNCTION guard_billing_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'billing receipt cannot be removed' USING ERRCODE='23514'; END IF;
 IF (to_jsonb(NEW)-'applied_at') IS DISTINCT FROM (to_jsonb(OLD)-'applied_at')
    OR OLD.applied_at IS NOT NULL OR NEW.applied_at IS NULL THEN
  RAISE EXCEPTION 'billing receipt identity and completion are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER billing_event_guard BEFORE UPDATE OR DELETE ON billing_events FOR EACH ROW EXECUTE FUNCTION guard_billing_event();
CREATE TRIGGER billing_events_no_truncate BEFORE TRUNCATE ON billing_events FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE FUNCTION guard_billing_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'billing state cannot be removed' USING ERRCODE='23514'; END IF;
 IF NEW.organization_id<>OLD.organization_id OR NEW.subscription_id<>OLD.subscription_id OR NEW.connection_id<>OLD.connection_id OR NEW.revision<>OLD.revision+1 THEN
  RAISE EXCEPTION 'billing state ownership/revision is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER billing_state_guard BEFORE UPDATE OR DELETE ON billing_subscription_state FOR EACH ROW EXECUTE FUNCTION guard_billing_state();
CREATE TRIGGER billing_state_no_truncate BEFORE TRUNCATE ON billing_subscription_state FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER billing_history_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON billing_subscription_history FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER billing_connections_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON billing_connections FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
CREATE TRIGGER billing_customers_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON billing_customers FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
