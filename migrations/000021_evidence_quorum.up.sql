BEGIN;
-- Capability identity only. Existing immutable bundles and organization grants
-- retain their published meaning; entitlement providers grant commercial access.
INSERT INTO feature_identities(feature_id, key) VALUES ('feat_0046', 'proof.quorum');
COMMIT;
