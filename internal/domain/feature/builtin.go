package feature

import "time"

// Stable capability identifiers; never rename or reuse a published key.
const (
	KeyProofQuorum              Key = "proof.quorum"
	KeyVerifyBasic              Key = "verify.basic"
	KeyVerifySecurity           Key = "verify.security"
	KeyVerifyAPI                Key = "verify.api"
	KeyVerifyPolyglot           Key = "verify.polyglot"
	KeyIntentBasic              Key = "intent.basic"
	KeyIntentAdvanced           Key = "intent.advanced"
	KeyIntentSpecforge          Key = "intent.specforge"
	KeyGraphLocal               Key = "graph.local"
	KeyGraphOrganization        Key = "graph.organization"
	KeyGraphCrossRepo           Key = "graph.cross_repo"
	KeyGraphGlobalChange        Key = "graph.global_change"
	KeyAutomationSafeUpgrade    Key = "automation.safe_upgrade"
	KeyProofPlanner             Key = "proof.planner"
	KeyProofPassport            Key = "proof.passport"
	KeyProofLiving              Key = "proof.living"
	KeyProofComposition         Key = "proof.composition"
	KeyTestTrust                Key = "testtrust"
	KeyRuntimeReplay            Key = "runtime.replay"
	KeyRuntimeBehaviorDNA       Key = "runtime.behaviordna"
	KeyRuntimeShadow            Key = "runtime.shadow"
	KeyBusinessInvariants       Key = "business.invariants"
	KeyIncidentAntibodies       Key = "incident.antibodies"
	KeyAgentProvenance          Key = "agent.provenance"
	KeyAgentMCPInventory        Key = "agent.mcp_inventory"
	KeyAgentMCPGateway          Key = "agent.mcp_gateway"
	KeyAgentSafeLease           Key = "agent.safelease"
	KeyAgentTemporalGuard       Key = "agent.temporalguard"
	KeyAgentEarnedAutonomy      Key = "agent.earned_autonomy"
	KeyEnterpriseSSO            Key = "enterprise.sso"
	KeyEnterpriseAudit          Key = "enterprise.audit"
	KeyEnterpriseSelfHosted     Key = "enterprise.self_hosted"
	KeyEnterpriseAirgap         Key = "enterprise.airgap"
	KeyEnterpriseLTS            Key = "enterprise.lts"
	KeyProofCloudHistory        Key = "proof.cloud_history"
	KeyIntegrationGitHubPrivate Key = "integration.github.private"
	KeyLanguageAdditionalPacks  Key = "language.additional_packs"
	KeyPlatformOrganizations    Key = "platform.organizations"
	KeyPolicyCentral            Key = "policy.central"
	KeyProofShared              Key = "proof.shared"
	KeyAuditOrganization        Key = "audit.organization"
	KeyVerifyPREnforcement      Key = "verify.pr_enforcement"
	KeyVerifyComposition        Key = "verify.composition"
	KeyEvidenceLocal            Key = "evidence.local"
	KeyPolicyLocalBasic         Key = "policy.local.basic"
	KeyPolicyLocalAdvanced      Key = "policy.local.advanced"
)

// NewBuiltinCatalog constructs the initial capability vocabulary. Version and
// time are explicit, reproducible catalog-registration metadata, not a claim
// that these planned capabilities are implemented in a released product.
// Persist the original values when storing these identities; do not reseed
// existing records with a new version or timestamp on every process start.
func NewBuiltinCatalog(introducedVersion string, createdAt time.Time) (*FeatureCatalog, error) {
	definitions := []struct {
		id                string
		key               Key
		name, description string
		category          Category
	}{
		{"feat_0001", KeyVerifyBasic, "Basic verification", "Baseline software-change verification.", CategoryVerify},
		{"feat_0002", KeyVerifySecurity, "Security verification", "Security-focused software-change checks.", CategorySecurity},
		{"feat_0003", KeyVerifyAPI, "API verification", "API compatibility and contract verification.", CategoryVerify},
		{"feat_0004", KeyVerifyPolyglot, "Polyglot verification", "Verification across multiple programming languages.", CategoryVerify},
		{"feat_0005", KeyIntentBasic, "Basic intent", "Capture the intended behavior of a software change.", CategoryIntent},
		{"feat_0006", KeyIntentAdvanced, "Advanced intent", "Analyze detailed change intent and constraints.", CategoryIntent},
		{"feat_0007", KeyIntentSpecforge, "SpecForge", "Derive reviewable specifications from change intent.", CategoryIntent},
		{"feat_0008", KeyGraphLocal, "Local graph", "Repository-local dependency and change relationships.", CategoryGraph},
		{"feat_0009", KeyGraphOrganization, "Organization graph", "Organization-scoped dependency relationships.", CategoryGraph},
		{"feat_0010", KeyGraphCrossRepo, "Cross-repository graph", "Relationships across authorized repositories.", CategoryGraph},
		{"feat_0011", KeyProofPlanner, "Proof planner", "Plan evidence needed to evaluate a change.", CategoryProof},
		{"feat_0012", KeyProofPassport, "Proof passport", "Collect portable proof and provenance records.", CategoryProof},
		{"feat_0013", KeyProofLiving, "Living proof", "Maintain proof relevance as software changes.", CategoryProof},
		{"feat_0014", KeyTestTrust, "Test trust", "Assess the trustworthiness of test evidence.", CategoryProof},
		{"feat_0015", KeyRuntimeReplay, "Runtime replay", "Orchestrate constrained replay of captured execution.", CategoryRuntime},
		{"feat_0016", KeyRuntimeBehaviorDNA, "BehaviorDNA", "Describe and compare observed runtime behavior.", CategoryRuntime},
		{"feat_0017", KeyRuntimeShadow, "Runtime shadow", "Observe changes through constrained shadow execution.", CategoryRuntime},
		{"feat_0018", KeyBusinessInvariants, "Business invariants", "Describe and evaluate business behavior invariants.", CategoryVerify},
		{"feat_0019", KeyIncidentAntibodies, "Incident antibodies", "Represent checks derived from prior incidents.", CategorySecurity},
		{"feat_0020", KeyAgentProvenance, "Agent provenance", "Track provenance of agent-produced changes and actions.", CategoryAgent},
		{"feat_0021", KeyAgentMCPInventory, "MCP inventory", "Inventory available MCP tools and capabilities.", CategoryAgent},
		{"feat_0022", KeyAgentMCPGateway, "MCP gateway", "Mediate agent tool requests through shared access boundaries.", CategoryAgent},
		{"feat_0023", KeyAgentSafeLease, "SafeLease", "Represent bounded authority for agent actions.", CategoryAgent},
		{"feat_0024", KeyAgentTemporalGuard, "TemporalGuard", "Describe time-bounded constraints for agent actions.", CategoryAgent},
		{"feat_0025", KeyEnterpriseSSO, "Single sign-on", "Integrate organization identity through single sign-on.", CategoryEnterprise},
		{"feat_0026", KeyEnterpriseAudit, "Audit controls", "Support organization audit and accountability workflows.", CategoryEnterprise},
		{"feat_0027", KeyEnterpriseSelfHosted, "Self-hosted deployment", "Operate the shared platform in a customer-managed environment.", CategoryEnterprise},
		{"feat_0028", KeyEnterpriseAirgap, "Air-gapped deployment", "Operate the shared platform without online dependencies.", CategoryEnterprise},
		{"feat_0029", KeyEnterpriseLTS, "Long-term support eligibility", "Represent eligibility for long-term support services without selecting a release channel.", CategoryEnterprise},
		{"feat_0030", KeyProofCloudHistory, "Cloud proof history", "Store proof history in hosted storage.", CategoryProof},
		{"feat_0031", KeyIntegrationGitHubPrivate, "Private GitHub integration", "Integrate authorized private GitHub repositories.", CategoryPlatform},
		{"feat_0032", KeyLanguageAdditionalPacks, "Additional language packs", "Use additional language-specific verification adapters.", CategoryPlatform},
		{"feat_0033", KeyPlatformOrganizations, "Organizations", "Organize users and resources within tenant boundaries.", CategoryPlatform},
		{"feat_0034", KeyPolicyCentral, "Central policy", "Manage shared verification policy centrally.", CategorySecurity},
		{"feat_0035", KeyProofShared, "Shared proofs", "Share proof records with authorized organization members.", CategoryProof},
		{"feat_0036", KeyAuditOrganization, "Organization audit", "Review organization-scoped audit records.", CategorySecurity},
		{"feat_0037", KeyVerifyPREnforcement, "Pull request enforcement", "Apply configured verification requirements to pull requests.", CategoryVerify},
		{"feat_0038", KeyVerifyComposition, "Composition verification", "Verify behavior across composed software components.", CategoryVerify},
		{"feat_0039", KeyEvidenceLocal, "Local evidence", "Read and retain evidence locally without hosted proof history or sharing.", CategoryProof},
		{"feat_0040", KeyPolicyLocalBasic, "Basic local policy", "Manage basic repository-local policy configuration.", CategorySecurity},
		{"feat_0041", KeyPolicyLocalAdvanced, "Advanced local policy", "Manage advanced repository-local policy configuration.", CategorySecurity},
		{"feat_0042", KeyProofComposition, "CompositionGuard", "Check pinned component proofs and evidence for their combined behavior.", CategoryProof},
		{"feat_0043", KeyGraphGlobalChange, "GlobalPatch", "Identify coordinated changes across authorized organization repositories.", CategoryGraph},
		{"feat_0044", KeyAutomationSafeUpgrade, "Verified dependency upgrades", "Verify pinned dependency upgrades through the shared proof workflow.", CategoryVerify},
		{"feat_0045", KeyAgentEarnedAutonomy, "Earned autonomy", "Grant bounded agent capabilities from fresh authoritative proof and operator policy.", CategoryAgent},
		{"feat_0046", KeyProofQuorum, "EvidenceQuorum", "Require independent verification families with explicit contradiction evidence.", CategoryProof},
	}
	features := make([]Feature, 0, len(definitions))
	for _, d := range definitions {
		features = append(features, Feature{
			FeatureID: d.id, Key: d.key, Name: d.name, Description: d.description,
			Category: d.category, Status: StatusPlanned,
			IntroducedVersion: introducedVersion, DefaultLimitType: LimitNone,
			RequiresFeatures: []Key{}, CreatedAt: createdAt,
		})
	}
	return NewFeatureCatalog(features)
}
