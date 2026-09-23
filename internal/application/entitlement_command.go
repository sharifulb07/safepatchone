package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

const entitlementUsage = "Usage: safepatch entitlement show [--json]\n       safepatch entitlement explain <feature> [--json]\n"

// Providers are trusted composition inputs, never selected by user flags or
// repository configuration. Runtime authorization remains a separate service.
type cliEntitlements interface {
	entitlement.EntitlementService
	Inspect(context.Context) (entitlement.CommunityReport, error)
}

type EntitlementExplanation struct {
	Feature              feature.Key            `json:"feature"`
	State                string                 `json:"state"`
	Edition              edition.Key            `json:"edition"`
	BundleVersion        string                 `json:"bundle_version"`
	Scope                string                 `json:"scope"`
	ImplementationStatus feature.Status         `json:"implementation_status"`
	Resolution           entitlement.Resolution `json:"resolution"`
}

type cliEdition struct {
	Edition       edition.Key `json:"edition"`
	Name          string      `json:"name"`
	BundleVersion string      `json:"bundle_version"`
	Scope         string      `json:"scope"`
}

type cliIdentity struct {
	Mode          string      `json:"mode"`
	Authenticated bool        `json:"authenticated"`
	Actor         *string     `json:"actor"`
	Organization  *string     `json:"organization"`
	Scope         string      `json:"scope"`
	Edition       edition.Key `json:"edition"`
}

func entitlementCommand(ctx context.Context, args []string, provider cliEntitlements, out cliOutput) int {
	if len(args) == 0 {
		return out.failure(&CLIError{Code: CodeInvalidUsage, Message: strings.TrimSpace(entitlementUsage)}, 2)
	}
	usage := entitlementUsage
	if args[0] == "edition" || args[0] == "whoami" {
		usage = "Usage: safepatch " + args[0] + " [--json]\n"
	}
	if (len(args) == 2 && helpArgument(args[1])) || (args[0] == "entitlement" && len(args) == 3 && (args[1] == "show" || args[1] == "explain") && helpArgument(args[2])) {
		return out.help(usage)
	}
	valid := (args[0] == "edition" || args[0] == "whoami") && len(args) == 1
	valid = valid || (args[0] == "entitlement" && ((len(args) == 2 && args[1] == "show") || (len(args) == 3 && args[1] == "explain")))
	if !valid {
		return out.failure(&CLIError{Code: CodeInvalidUsage, Message: strings.TrimSpace(usage)}, 2)
	}
	if args[0] == "entitlement" && args[1] == "explain" && feature.ValidateKey(feature.Key(args[2])) != nil {
		return out.failure(&CLIError{Code: CodeInvalidUsage, Message: "feature must be a valid stable capability key"}, 2)
	}
	if provider == nil {
		return out.failure(&CLIError{Code: CodeEntitlementUnavailable, Message: "local entitlement inspection unavailable"}, 1)
	}
	if args[0] == "entitlement" && args[1] == "explain" {
		return explainEntitlement(ctx, provider, feature.Key(args[2]), out)
	}
	report, err := provider.Inspect(ctx)
	if err != nil {
		return out.failure(&CLIError{Code: CodeEntitlementUnavailable, Message: "local entitlement inspection unavailable", cause: err}, 1)
	}
	metadata, err := edition.Lookup(report.Edition)
	if err != nil {
		return out.failure(&CLIError{Code: CodeEntitlementUnavailable, Message: "local entitlement metadata unavailable", cause: err}, 1)
	}
	switch args[0] {
	case "edition":
		return out.success(cliEdition{Edition: report.Edition, Name: metadata.Name, BundleVersion: report.BundleVersion, Scope: report.Scope}, fmt.Sprintf("Edition:\n%s\nBundle version: %s\nScope: %s\n", metadata.Name, report.BundleVersion, report.Scope))
	case "whoami":
		// Never infer an authenticated owner from an OS name or token variable.
		return out.success(cliIdentity{Mode: "offline", Scope: report.Scope, Edition: report.Edition}, fmt.Sprintf("Identity: local offline session\nAuthenticated account: none\nOrganization: none\nScope: %s\nEdition: %s\n", report.Scope, metadata.Name))
	default:
		var text strings.Builder
		fmt.Fprintf(&text, "Edition:\n%s\nBundle version: %s\nSource: %s\nScope: %s\nAccount/subscription: not required\nNetwork: not required\n\nCapabilities:\n", metadata.Name, report.BundleVersion, report.Source, report.Scope)
		for _, f := range report.Features {
			fmt.Fprintf(&text, "  %s: %s (implementation: %s)\n", f.Key, entitlementState(f.Entitled), f.ImplementationStatus)
		}
		text.WriteString("\nOther capabilities are not granted locally; cloud-only features are unavailable.\nFeature limits: unspecified; no numeric quotas are inferred.\nEntitlement does not imply implementation or actor permission.\n")
		return out.success(report, text.String())
	}
}

func explainEntitlement(ctx context.Context, provider cliEntitlements, key feature.Key, out cliOutput) int {
	resolution, err := provider.Resolve(ctx, entitlement.LocalOrganizationID, key)
	if err != nil {
		if errors.Is(err, entitlement.ErrUnknownFeature) {
			return out.failure(&CLIError{Code: CodeUnknownFeature, Message: "feature is not registered", Feature: key, cause: err}, 2)
		}
		return out.failure(&CLIError{Code: CodeEntitlementUnavailable, Message: "feature entitlement could not be resolved", Feature: key, cause: err}, 1)
	}
	report, err := provider.Inspect(ctx)
	if err != nil {
		return out.failure(&CLIError{Code: CodeEntitlementUnavailable, Message: "local entitlement inspection unavailable", cause: err}, 1)
	}
	for _, f := range report.Features {
		if f.Key != key {
			continue
		}
		if f.Entitled != resolution.Enabled {
			break
		}
		result := EntitlementExplanation{Feature: key, State: entitlementState(resolution.Enabled), Edition: report.Edition, BundleVersion: report.BundleVersion, Scope: report.Scope, ImplementationStatus: f.ImplementationStatus, Resolution: resolution}
		if !resolution.Enabled {
			return out.failure(&CLIError{Code: CodeFeatureNotEntitled, Message: "feature is disabled in the effective entitlement bundle", Feature: key, Entitlement: &result, cause: entitlement.ErrNotEntitled}, 1)
		}
		return out.success(result, explainText(result))
	}
	return out.failure(&CLIError{Code: CodeEntitlementUnavailable, Message: "feature entitlement metadata is inconsistent", Feature: key}, 1)
}

func entitlementState(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func explainText(e EntitlementExplanation) string {
	source := string(e.Resolution.Source)
	if source == "" {
		source = "none"
	}
	limit := "unspecified"
	if e.Resolution.EffectiveLimit != nil {
		limit = fmt.Sprint(*e.Resolution.EffectiveLimit)
	}
	return fmt.Sprintf("%s: %s\nBundle version: %s\nScope: %s\nSource: %s\nFeature limit: %s\nImplementation: %s\nReason: %s\nEntitlement does not imply implementation or actor permission.\n", e.Feature, e.State, e.BundleVersion, e.Scope, source, limit, e.ImplementationStatus, e.Resolution.Explanation)
}
