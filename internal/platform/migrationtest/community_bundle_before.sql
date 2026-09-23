CREATE TEMP TABLE community_history_before AS
    SELECT * FROM edition_features;
INSERT INTO quota_accounts VALUES ('org_pinned');
INSERT INTO entitlement_limits VALUES ('org_pinned', 'repositories', 'INTEGER', 2);
INSERT INTO quota_usage VALUES ('org_pinned', 'repositories', 'lifetime', 0, 1);
INSERT INTO quota_reservations VALUES ('org_pinned', 'existing_repo', 'repositories', 'lifetime', 1, 'COMMITTED');
