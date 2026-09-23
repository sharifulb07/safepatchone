// Package licensing is the boundary for validating licenses and translating them into effective entitlement inputs. It must not duplicate verification or authorization logic.
// EnterpriseLicenseEntitlementProvider verifies signed documents with public keys
// and implements the existing organization EntitlementService. No license signing
// private key or license signing operation belongs in a self-hosted node.
// Connected deployments can use a separate LicenseRefresher with bounded outage
// grace anchored in persistent state. Air-gapped deployments have no refresher.
package licensing
