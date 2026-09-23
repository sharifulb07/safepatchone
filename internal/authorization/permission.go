package authorization

import "errors"

type Key string

const (
	KeyProofExecute       Key = "proof.execute"
	KeyProofView          Key = "proof.view"
	KeyPolicyView         Key = "policy.view"
	KeyPolicyManage       Key = "policy.manage"
	KeyReplayExecute      Key = "replay.execute"
	KeyReplayView         Key = "replay.view"
	KeyAgentApprove       Key = "agent.approve"
	KeyAgentManage        Key = "agent.manage"
	KeyAuditView          Key = "audit.view"
	KeyBillingManage      Key = "billing.manage"
	KeyOrganizationManage Key = "organization.manage"
)

var ErrUnknownPermission = errors.New("unknown permission key")

func Keys() []Key {
	return []Key{KeyProofExecute, KeyProofView, KeyPolicyView, KeyPolicyManage, KeyReplayExecute, KeyReplayView, KeyAgentApprove, KeyAgentManage, KeyAuditView, KeyBillingManage, KeyOrganizationManage}
}

func ValidateKey(key Key) error {
	for _, known := range Keys() {
		if key == known {
			return nil
		}
	}
	return ErrUnknownPermission
}

type Role string

const (
	RoleOwner  Role = "owner"
	RoleViewer Role = "viewer"
)

// Explicit finite grants: adding a permission key does not silently grant it to
// every owner or viewer. Roles describe membership, never commercial editions.
func roleAllows(role Role, key Key) bool {
	switch role {
	case RoleOwner:
		switch key {
		case KeyProofExecute, KeyProofView, KeyPolicyView, KeyPolicyManage, KeyReplayExecute, KeyReplayView, KeyAgentApprove, KeyAgentManage, KeyAuditView, KeyBillingManage, KeyOrganizationManage:
			return true
		}
	case RoleViewer:
		switch key {
		case KeyProofView, KeyPolicyView, KeyReplayView, KeyAuditView:
			return true
		}
	}
	return false
}
