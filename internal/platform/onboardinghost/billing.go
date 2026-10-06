package onboardinghost

import "safepatch/internal/domain/billing"

// Preserve existing Stripe-only installations; never silently select between
// two configured accounts or start with an explicit provider missing its key.
func billingProvider(get func(string) string) (string, error) {
	p := get("SAFEPATCH_BILLING_PROVIDER")
	stripe, paddle := get("SAFEPATCH_STRIPE_API_KEY") != "", get("SAFEPATCH_PADDLE_API_KEY") != ""
	if stripe && paddle {
		return "", billing.ErrInvalidConfiguration
	}
	if p == "" {
		if stripe {
			return "stripe", nil
		}
		if paddle {
			return "", billing.ErrInvalidConfiguration
		}
		return "", nil
	}
	if p == "stripe" && stripe {
		return p, nil
	}
	if p == "paddle" && paddle {
		return p, nil
	}
	return "", billing.ErrInvalidConfiguration
}
