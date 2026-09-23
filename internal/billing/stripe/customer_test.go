package stripe_test

import (
	"net/http"
	"safepatch/internal/domain/billing"
	"testing"
)

func TestOrganizationCustomerProvisioningIsIdempotentAndFailClosed(t *testing.T) {
	for _, body := range []string{`{"id":"cus_a","livemode":false}`, `{"id":"cus_a","livemode":true}`, `{"id":"cus_a"}`, `{"id":"sub_a","livemode":false}`} {
		t.Run(body, func(t *testing.T) {
			c := config(t)
			calls := 0
			c.Transport = transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "api.stripe.com" || r.URL.Path != "/v1/customers" || r.Method != "POST" {
					t.Fatal("unexpected destination", r.URL)
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Form.Get("metadata[organization_id]") != "org_a" || r.Header.Get("Idempotency-Key") != billing.StableID("customer", "org_a") {
					t.Fatal("customer lacks stable organization binding")
				}
				return response(200, body), nil
			})
			a := adapter(t, c)
			for range 2 {
				id, err := a.CreateOrganizationCustomer(t.Context(), "org_a")
				if body == `{"id":"cus_a","livemode":false}` {
					if err != nil || id != "cus_a" {
						t.Fatal(id, err)
					}
				} else if err == nil {
					t.Fatal("malformed provider state accepted")
				}
			}
			if _, err := a.CreateOrganizationCustomer(t.Context(), "../../foreign"); err == nil || calls != 2 {
				t.Fatal("invalid tenant reached provider")
			}
		})
	}
}
