package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"safepatch/internal/domain/billing"
	"time"
)

type BillingWebhookProcessor interface {
	Handle(context.Context, billing.WebhookDelivery) error
}

func NewBillingWebhookHandler(processor BillingWebhookProcessor) (http.Handler, error) {
	return NewBillingWebhookHandlerWithSignature(processor, "Stripe-Signature")
}

func NewBillingWebhookHandlerWithSignature(processor BillingWebhookProcessor, signatureHeader string) (http.Handler, error) {
	if processor == nil || (signatureHeader != "Stripe-Signature" && signatureHeader != "Paddle-Signature") {
		return nil, billing.ErrInvalidConfiguration
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if len(r.Header.Values(signatureHeader)) != 1 {
			http.Error(w, "invalid webhook", 400)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
		defer r.Body.Close()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid webhook body", 413)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		err = processor.Handle(ctx, billing.WebhookDelivery{Payload: raw, Signature: r.Header.Get(signatureHeader)})
		switch {
		case err == nil, errors.Is(err, billing.ErrUnsupportedEvent):
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, billing.ErrUnverifiedEvent), errors.Is(err, billing.ErrInvalidData):
			http.Error(w, "invalid webhook", 400)
		default:
			http.Error(w, "billing synchronization unavailable", 503)
		}
	}), nil
}
