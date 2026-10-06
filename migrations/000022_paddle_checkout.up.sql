BEGIN;
-- Server-created transaction identity, independent of editable checkout metadata.
CREATE TABLE paddle_checkouts (
 connection_id text NOT NULL REFERENCES billing_connections(connection_id) ON DELETE RESTRICT,
 checkout_id text NOT NULL CHECK(checkout_id ~ '^bill_[a-f0-9]{64}$'),
 organization_id text NOT NULL REFERENCES organizations(organization_id) ON DELETE RESTRICT,
 offer_id text NOT NULL CHECK(offer_id ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$'),
 transaction_id text CHECK(transaction_id ~ '^txn_[a-z0-9]{26}$'),
 subscription_id text CHECK(subscription_id ~ '^sub_[a-z0-9]{26}$'),
 checkout_url text,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(connection_id,checkout_id),
 UNIQUE(connection_id,transaction_id), UNIQUE(connection_id,subscription_id),
 CHECK((transaction_id IS NULL) = (checkout_url IS NULL)),
 CHECK(subscription_id IS NULL OR transaction_id IS NOT NULL),
 CHECK(checkout_url IS NULL OR (checkout_url LIKE 'https://%' AND octet_length(checkout_url)<4096))
);
CREATE INDEX paddle_checkout_organization ON paddle_checkouts(connection_id,organization_id);
CREATE FUNCTION guard_paddle_checkout() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP <> 'UPDATE' THEN RAISE EXCEPTION 'checkout history is immutable' USING ERRCODE='23514'; END IF;
 IF (to_jsonb(NEW)-'transaction_id'-'checkout_url'-'subscription_id') IS DISTINCT FROM (to_jsonb(OLD)-'transaction_id'-'checkout_url'-'subscription_id')
 OR (OLD.transaction_id IS NOT NULL AND (NEW.transaction_id IS DISTINCT FROM OLD.transaction_id OR NEW.checkout_url IS DISTINCT FROM OLD.checkout_url))
 OR (OLD.subscription_id IS NOT NULL AND NEW.subscription_id IS DISTINCT FROM OLD.subscription_id) THEN
 RAISE EXCEPTION 'checkout ownership is immutable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER paddle_checkout_guard BEFORE UPDATE OR DELETE ON paddle_checkouts FOR EACH ROW EXECUTE FUNCTION guard_paddle_checkout();
CREATE TRIGGER paddle_checkout_no_truncate BEFORE TRUNCATE ON paddle_checkouts FOR EACH STATEMENT EXECUTE FUNCTION reject_edition_history_change();
COMMIT;
