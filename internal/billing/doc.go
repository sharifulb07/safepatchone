// Package billing is the commercial adapter boundary. Provider events and SKU mappings belong here, never in core domain or application packages.
// Provider-neutral ports live in internal/domain/billing; concrete Stripe code
// lives in internal/billing/stripe. No provider response directly issues grants.
package billing
