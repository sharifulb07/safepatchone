BEGIN;

-- Add capability identities without modifying the published original registry.
INSERT INTO feature_identities (feature_id, key) VALUES
('feat_0030', 'proof.cloud_history'),
('feat_0031', 'integration.github.private'),
('feat_0032', 'language.additional_packs'),
('feat_0033', 'platform.organizations'),
('feat_0034', 'policy.central'),
('feat_0035', 'proof.shared'),
('feat_0036', 'audit.organization'),
('feat_0037', 'verify.pr_enforcement'),
('feat_0038', 'verify.composition');

-- Canonical initial bundle data. All inheritance is materialized here; readers
-- never infer grants from edition names, ordering, or a mutable parent bundle.
-- NULL limits are unspecified, not unlimited. Features remain PLANNED.
-- Intentionally no upsert: any ID conflict rolls back this entire migration.
DO $seed$
DECLARE bundle jsonb;
BEGIN
    FOR bundle IN SELECT value FROM jsonb_array_elements($bundles$
[
  {
    "version": {
      "edition_version_id": "community-2026-v1",
      "edition_key": "community",
      "created_at": "2026-09-06T00:00:00Z"
    },
    "features": [
      {
        "edition_version_id": "community-2026-v1",
        "feature_id": "feat_0001",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "community-2026-v1",
        "feature_id": "feat_0002",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "community-2026-v1",
        "feature_id": "feat_0005",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "community-2026-v1",
        "feature_id": "feat_0008",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      }
    ]
  },
  {
    "version": {
      "edition_version_id": "developer-2026-v1",
      "edition_key": "developer",
      "created_at": "2026-09-06T00:00:00Z"
    },
    "features": [
      {
        "edition_version_id": "developer-2026-v1",
        "feature_id": "feat_0001",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "developer-2026-v1",
        "feature_id": "feat_0002",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "developer-2026-v1",
        "feature_id": "feat_0005",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "developer-2026-v1",
        "feature_id": "feat_0006",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "developer-2026-v1",
        "feature_id": "feat_0008",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "developer-2026-v1",
        "feature_id": "feat_0011",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "developer-2026-v1",
        "feature_id": "feat_0030",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "developer-2026-v1",
        "feature_id": "feat_0031",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "developer-2026-v1",
        "feature_id": "feat_0032",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      }
    ]
  },
  {
    "version": {
      "edition_version_id": "team-2026-v1",
      "edition_key": "team",
      "created_at": "2026-09-06T00:00:00Z"
    },
    "features": [
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0001",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0002",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0005",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0006",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0008",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0009",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0011",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0012",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0030",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0031",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0032",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0033",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0034",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0035",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0036",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "team-2026-v1",
        "feature_id": "feat_0037",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      }
    ]
  },
  {
    "version": {
      "edition_version_id": "business-2026-v1",
      "edition_key": "business",
      "created_at": "2026-09-06T00:00:00Z"
    },
    "features": [
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0001",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0002",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0005",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0006",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0008",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0009",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0010",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0011",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0012",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0014",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0015",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0016",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0018",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0019",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0030",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0031",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0032",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0033",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0034",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0035",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0036",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0037",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "business-2026-v1",
        "feature_id": "feat_0038",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      }
    ]
  },
  {
    "version": {
      "edition_version_id": "enterprise-2026-v1",
      "edition_key": "enterprise",
      "created_at": "2026-09-06T00:00:00Z"
    },
    "features": [
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0001",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0002",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0005",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0006",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0008",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0009",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0010",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0011",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0012",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0013",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0014",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0015",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0016",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0018",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0019",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0022",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0023",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0024",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0025",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0026",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0027",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0028",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0029",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0030",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0031",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0032",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0033",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0034",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0035",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0036",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0037",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      },
      {
        "edition_version_id": "enterprise-2026-v1",
        "feature_id": "feat_0038",
        "enabled": true,
        "limit_value": null,
        "configuration_json": {}
      }
    ]
  }
]
$bundles$::jsonb) LOOP
        PERFORM publish_edition_version(
            bundle->'version'->>'edition_version_id',
            bundle->'version'->>'edition_key',
            (bundle->'version'->>'created_at')::timestamptz,
            bundle->'features'
        );
    END LOOP;
END;
$seed$;

COMMIT;
