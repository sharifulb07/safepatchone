// Package application coordinates shared use cases through domain contracts.
// Infrastructure is supplied by composition roots, never imported here.
package application

import (
	"context"
	"fmt"
	"io"

	"safepatch/internal/domain"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

// Run provides shared command routing and local entitlement inspection.
// Exit codes are 0 for help/inspection, 1 for unavailable operations or output failures,
// and 2 for invalid usage. It does not start services or perform verification.
func Run(command string, args []string, stdout, stderr io.Writer) int {
	return RunWithVersionMetadata(command, args, stdout, stderr, localVersionMetadata{})
}

// RunWithVersionMetadata lets trusted composition roots supply resolved version
// inspection metadata. The provider cannot grant capabilities or actor permissions
// and is used only by version; other commands retain their existing providers.
func RunWithVersionMetadata(command string, args []string, stdout, stderr io.Writer, provider VersionMetadataProvider) int {
	args, jsonMode, valid := cliArguments(args)
	out := cliOutput{stdout: stdout, stderr: stderr, json: jsonMode}
	if !valid {
		return out.failure(&CLIError{Code: CodeInvalidUsage, Message: "--json may be specified only once"}, 2)
	}
	switch command {
	case "safepatch", "safepatch-api", "safepatch-worker",
		"safepatch-collector", "safepatch-mcp-gateway", "safepatch-replay":
	default:
		return out.failure(&CLIError{Code: CodeInvalidUsage, Message: "unknown SafePatch command"}, 2)
	}
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		return versionCommand(context.Background(), provider, out)
	}

	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		usage := "[--help] | version [--json]"
		if command == "safepatch" {
			usage = "[--help] | version | entitlement show | entitlement explain <feature> | edition | whoami [--json]"
		}
		if command == "safepatch-api" {
			return out.help("Usage: safepatch-api [--help] | version [--json] | serve\nServe organization onboarding with trusted PostgreSQL, GitHub App and NATS configuration.\n")
		}
		return out.help(fmt.Sprintf("Usage: %s %s\nShared SafePatch command shell; verification and server behavior are not implemented yet.\n", command, usage))
	}

	if command == "safepatch" && len(args) > 0 && (args[0] == "entitlement" || args[0] == "edition" || args[0] == "whoami") {
		provider, err := entitlement.NewCommunityEntitlementProvider()
		if err != nil {
			return out.failure(&CLIError{Code: CodeEntitlementUnavailable, Message: "local entitlement inspection unavailable", cause: err}, 1)
		}
		return entitlementCommand(context.Background(), args, provider, out)
	}

	if len(args) == 0 || (command == "safepatch" && len(args) == 1 && args[0] == "verify") {
		if command == "safepatch" && len(args) == 1 {
			provider, err := entitlement.NewCommunityEntitlementProvider()
			if err != nil {
				return out.failure(&CLIError{Code: CodeEntitlementUnavailable, Message: "local entitlement inspection unavailable", cause: err}, 1)
			}
			if err := RequireFeature(context.Background(), provider, entitlement.LocalOrganizationID, feature.KeyVerifyBasic); err != nil {
				return out.failure(err.(*CLIError), 1)
			}
		}
		return out.failure(&CLIError{Code: CodeNotImplemented, Message: domain.ErrNotImplemented.Error(), cause: domain.ErrNotImplemented}, 1)
	}

	return out.failure(&CLIError{Code: CodeInvalidUsage, Message: fmt.Sprintf("Usage: %s [--help]", command)}, 2)
}
