package onboardinghost

import "testing"

func TestBillingProviderSelection(t *testing.T) {
	for _, tc := range []struct {
		p, s, k, want string
		fail          bool
	}{{"", "", "", "", false}, {"", "stripe", "", "stripe", false}, {"paddle", "", "paddle", "paddle", false}, {"stripe", "stripe", "", "stripe", false}, {"", "", "paddle", "", true}, {"paddle", "stripe", "paddle", "", true}, {"paddle", "", "", "", true}, {"unknown", "", "", "", true}} {
		got, err := billingProvider(func(k string) string {
			switch k {
			case "SAFEPATCH_BILLING_PROVIDER":
				return tc.p
			case "SAFEPATCH_STRIPE_API_KEY":
				return tc.s
			case "SAFEPATCH_PADDLE_API_KEY":
				return tc.k
			}
			return ""
		})
		if got != tc.want || (err != nil) != tc.fail {
			t.Fatal(tc, got, err)
		}
	}
}
