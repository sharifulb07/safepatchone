package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"safepatch/internal/domain/billing"
	"strings"
	"testing"
)

type webhookFunc func(context.Context, billing.WebhookDelivery) error

func (f webhookFunc) Handle(c context.Context, d billing.WebhookDelivery) error { return f(c, d) }
func TestPaddleWebhookUsesOnlyPaddleSignature(t *testing.T) {
	calls := 0
	h, err := NewBillingWebhookHandlerWithSignature(webhookFunc(func(_ context.Context, d billing.WebhookDelivery) error {
		calls++
		if d.Signature != "paddle-signature" || string(d.Payload) != " { } " {
			t.Fatal(d)
		}
		return nil
	}), "Paddle-Signature")
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"Stripe-Signature", "Paddle-Signature"} {
		r := httptest.NewRequest("POST", "/v1/billing/paddle/webhook", strings.NewReader(" { } "))
		r.Header.Set(header, "paddle-signature")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 400
		if header == "Paddle-Signature" {
			want = 204
		}
		if w.Code != want {
			t.Fatal(w.Code)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestBillingWebhookIngressStatusAndBodyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{{"applied", nil, 204}, {"ignored", billing.ErrUnsupportedEvent, 204}, {"signature", billing.ErrUnverifiedEvent, 400}, {"bad_data", billing.ErrInvalidData, 400}, {"unknown_price", billing.ErrUnsupported, 503}, {"storage", errors.New("private credentials"), 503}, {"oversize", nil, 413}, {"method", nil, 405}, {"duplicate_header", nil, 400}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler, _ := NewBillingWebhookHandler(webhookFunc(func(_ context.Context, d billing.WebhookDelivery) error {
				calls++
				if string(d.Payload) != " { } " || d.Signature != "signed" {
					t.Error("body rewritten")
				}
				return tc.err
			}))
			body := " { } "
			if tc.name == "oversize" {
				body = strings.Repeat("x", 256*1024+1)
			}
			method := "POST"
			if tc.name == "method" {
				method = "GET"
			}
			req := httptest.NewRequest(method, "/v1/billing/stripe/webhook", strings.NewReader(body))
			req.Header.Set("Stripe-Signature", "signed")
			if tc.name == "duplicate_header" {
				req.Header.Add("Stripe-Signature", "second")
			}
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			if out.Code != tc.status || strings.Contains(out.Body.String(), "private") {
				t.Fatal(out.Code, out.Body.String())
			}
			if (tc.name == "oversize" || tc.name == "method" || tc.name == "duplicate_header") && calls != 0 {
				t.Fatal("invalid ingress reached processor")
			}
		})
	}
}
