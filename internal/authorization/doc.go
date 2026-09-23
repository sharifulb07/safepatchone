// Package authorization defines actor and resource permission contracts, separate from organization entitlements. Owning a feature does not authorize an actor.
// PermissionService resolves explicit owner/viewer role grants from current
// tenant-scoped membership. It does not depend on subscription or edition data.
package authorization
