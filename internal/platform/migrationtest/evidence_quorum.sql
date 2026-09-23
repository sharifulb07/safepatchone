DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM feature_identities WHERE feature_id='feat_0046' AND key='proof.quorum') THEN
  RAISE EXCEPTION 'EvidenceQuorum identity missing';
 END IF;
 IF EXISTS (SELECT 1 FROM edition_features WHERE feature_id='feat_0046') THEN
  RAISE EXCEPTION 'Identity registration silently changed commercial grants';
 END IF;
END;
$$;
