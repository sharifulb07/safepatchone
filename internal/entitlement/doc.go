// Package entitlement defines organization-scoped entitlement repository
// contracts. The canonical record lives in internal/domain/entitlement.
// EntitlementResolver evaluates source precedence and temporal eligibility.
// SnapshotService caches effective decisions with bounded validity and checks
// authoritative source revisions before every use.
// Infrastructure adapters are not yet implemented; actor authorization remains
// a separate boundary.
package entitlement
