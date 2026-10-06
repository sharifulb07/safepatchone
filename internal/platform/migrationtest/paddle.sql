INSERT INTO organizations VALUES ('paddle_a','A','ACTIVE','2026-09-27T12:00:00Z'),('paddle_b','B','ACTIVE','2026-09-27T12:00:00Z');
INSERT INTO billing_connections VALUES ('paddle_sandbox','paddle','seller_fixture',false);
INSERT INTO paddle_checkouts(connection_id,checkout_id,organization_id,offer_id) VALUES ('paddle_sandbox','bill_'||repeat('a',64),'paddle_a','team');
UPDATE paddle_checkouts SET transaction_id='txn_01h04vsc0qhwtsbsxh3422wjs4',checkout_url='https://example.test/checkout?_ptxn=txn_01h04vsc0qhwtsbsxh3422wjs4' WHERE organization_id='paddle_a';
UPDATE paddle_checkouts SET subscription_id='sub_01h04vsc0qhwtsbsxh3422wjs4' WHERE organization_id='paddle_a';
DO $$ BEGIN
 BEGIN UPDATE paddle_checkouts SET organization_id='paddle_b'; RAISE EXCEPTION 'ownership mutable'; EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN UPDATE paddle_checkouts SET transaction_id='txn_01h04vsc0qhwtsbsxh3422wjs5'; RAISE EXCEPTION 'transaction mutable'; EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN UPDATE paddle_checkouts SET subscription_id=NULL; RAISE EXCEPTION 'subscription mutable'; EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN DELETE FROM paddle_checkouts; RAISE EXCEPTION 'history deletable'; EXCEPTION WHEN check_violation THEN NULL; END;
 BEGIN INSERT INTO paddle_checkouts(connection_id,checkout_id,organization_id,offer_id,transaction_id,checkout_url) VALUES ('paddle_sandbox','bill_'||repeat('b',64),'paddle_b','team','txn_01h04vsc0qhwtsbsxh3422wjs4','https://example.test/checkout'); RAISE EXCEPTION 'duplicate transaction'; EXCEPTION WHEN unique_violation THEN NULL; END;
END; $$;
