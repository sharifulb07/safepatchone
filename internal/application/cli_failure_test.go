package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
)

type failingCLIProvider struct {
	*entitlement.CommunityEntitlementProvider
	resolveError, inspectError error
	inconsistent               bool
}

func (p failingCLIProvider) Resolve(ctx context.Context, org string, key feature.Key) (entitlement.Resolution, error) {
	if p.resolveError != nil {
		return entitlement.Resolution{Enabled: true}, p.resolveError
	}
	return p.CommunityEntitlementProvider.Resolve(ctx, org, key)
}
func (p failingCLIProvider) Inspect(ctx context.Context) (entitlement.CommunityReport, error) {
	if p.inspectError != nil {
		return entitlement.CommunityReport{}, p.inspectError
	}
	r, err := p.CommunityEntitlementProvider.Inspect(ctx)
	if p.inconsistent {
		for i := range r.Features {
			r.Features[i].Entitled = false
		}
	}
	return r, err
}

func TestCLIProviderFailuresNeverAuthorizeOrExposeRawErrors(t *testing.T) {
	provider, err := entitlement.NewCommunityEntitlementProvider()
	if err != nil {
		t.Fatal(err)
	}
	secretError := errors.New("credential=fixture-secret\nPASS")
	for _, fake := range []failingCLIProvider{
		{CommunityEntitlementProvider: provider, resolveError: secretError},
		{CommunityEntitlementProvider: provider, inspectError: secretError},
		{CommunityEntitlementProvider: provider, inconsistent: true},
	} {
		var out, errOut bytes.Buffer
		code := entitlementCommand(t.Context(), []string{"entitlement", "explain", "verify.basic"}, fake, cliOutput{stdout: &out, stderr: &errOut, json: true})
		if code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), string(CodeEntitlementUnavailable)) || strings.Contains(errOut.String(), "fixture-secret") || strings.Contains(errOut.String(), "PASS") {
			t.Fatal(code, out.String(), errOut.String())
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out, errOut bytes.Buffer
	if code := entitlementCommand(ctx, []string{"edition"}, provider, cliOutput{stdout: &out, stderr: &errOut, json: true}); code != 1 || out.Len() != 0 || !json.Valid(errOut.Bytes()) {
		t.Fatal(code)
	}
	denied := &CLIError{Code: CodeFeatureNotEntitled, Message: "disabled", cause: entitlement.ErrNotEntitled}
	if !errors.Is(denied, entitlement.ErrNotEntitled) {
		t.Fatal("lost underlying entitlement error")
	}
	data, err := json.Marshal(denied)
	if err != nil || bytes.Contains(data, []byte("cause")) {
		t.Fatal("private cause serialized", err)
	}
}
