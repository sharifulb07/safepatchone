// Package tenant owns tenant identity and ownership invariants. Tenant scope must flow through application contracts and storage adapters.
// Scope validates bounded resource/organization identities. Trusted resolution
// of membership and resource ownership is implemented by organizationstore for
// the shared PostgreSQL SaaS entities; authentication remains composition-owned.
package tenant
