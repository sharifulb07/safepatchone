package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/jsonobject"
	org "safepatch/internal/domain/organization"
)

type event struct {
	ID, Object, Type string
	APIVersion       string `json:"api_version"`
	Account          string // Connect events are outside this single-account adapter.
	LiveMode         *bool  `json:"livemode"`
	Created          int64
	Data             struct{ Object json.RawMessage }
}
type verifiedEvent struct {
	adapter  *StripeBillingAdapter
	signedAt time.Time
	event    event
}

func (*verifiedEvent) Provider() string { return Provider }

func (a *StripeBillingAdapter) VerifyWebhook(ctx context.Context, delivery billing.WebhookDelivery) (billing.VerifiedWebhookEvent, error) {
	if err := a.check(ctx); err != nil {
		return nil, err
	}
	if len(delivery.Payload) == 0 || len(delivery.Payload) > MaxPayloadBytes || len(delivery.Signature) == 0 || len(delivery.Signature) > 4096 {
		return nil, billing.ErrUnverifiedEvent
	}
	// Retain no caller-owned buffers and authenticate the exact, unnormalized body.
	body := append([]byte(nil), delivery.Payload...)
	var stamp string
	var signatures [][]byte
	for _, part := range strings.Split(delivery.Signature, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, billing.ErrUnverifiedEvent
		}
		switch key {
		case "t":
			if stamp != "" {
				return nil, billing.ErrUnverifiedEvent
			}
			stamp = value
		case "v1":
			sig, err := hex.DecodeString(value)
			if err != nil || len(sig) != sha256.Size || len(signatures) >= 8 {
				return nil, billing.ErrUnverifiedEvent
			}
			signatures = append(signatures, sig)
		}
	}
	ts, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || ts <= 0 || strconv.FormatInt(ts, 10) != stamp || len(signatures) == 0 {
		return nil, billing.ErrUnverifiedEvent
	}
	signedAt := time.Unix(ts, 0).UTC()
	if !a.fresh(signedAt) {
		return nil, billing.ErrUnverifiedEvent
	}
	valid := false
	for _, secret := range a.secrets {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(stamp + "."))
		mac.Write(body)
		digest := mac.Sum(nil)
		for _, signature := range signatures {
			if hmac.Equal(digest, signature) {
				valid = true
			}
		}
	}
	if !valid {
		return nil, billing.ErrUnverifiedEvent
	}
	checked, err := jsonobject.Normalize(body, MaxPayloadBytes)
	if err != nil {
		return nil, billing.ErrInvalidData
	}
	var e event
	if json.Unmarshal(checked, &e) != nil || !providerID(e.ID, "evt_") || e.Object != "event" || e.APIVersion != APIVersion || e.Account != "" || e.LiveMode == nil || *e.LiveMode != a.live || e.Created <= 0 || e.Created > signedAt.Add(SignatureTolerance).Unix() {
		return nil, billing.ErrInvalidData
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &verifiedEvent{adapter: a, signedAt: signedAt, event: e}, nil
}
func (a *StripeBillingAdapter) fresh(t time.Time) bool {
	now := a.now()
	return !t.Before(now.Add(-SignatureTolerance)) && !t.After(now.Add(SignatureTolerance))
}
func (a *StripeBillingAdapter) ProcessVerifiedWebhookEvent(ctx context.Context, handle billing.VerifiedWebhookEvent) (billing.SubscriptionEvent, error) {
	fail := billing.SubscriptionEvent{}
	if err := a.check(ctx); err != nil {
		return fail, err
	}
	v, ok := handle.(*verifiedEvent)
	if !ok || v == nil || v.adapter != a || !a.fresh(v.signedAt) {
		return fail, billing.ErrUnverifiedEvent
	}
	e := v.event
	switch e.Type {
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
	default:
		return fail, billing.ErrUnsupported
	}
	var s subscription
	if json.Unmarshal(e.Data.Object, &s) != nil || !providerID(s.Customer, "cus_") {
		return fail, billing.ErrInvalidData
	}
	organizationID, err := a.customers.OrganizationForCustomer(ctx, Provider, s.Customer)
	if ctx.Err() != nil {
		return fail, ctx.Err()
	}
	if err != nil || !org.ValidID(organizationID) {
		return fail, billing.ErrOwnership
	}
	snapshot, err := a.normalize(ctx, s, organizationID)
	if err != nil {
		return fail, err
	}
	if e.Type == "customer.subscription.deleted" && snapshot.State != org.Canceled {
		return fail, billing.ErrInvalidData
	}
	if ctx.Err() != nil {
		return fail, ctx.Err()
	}
	return billing.SubscriptionEvent{Provider: Provider, EventID: e.ID, OccurredAt: time.Unix(e.Created, 0).UTC(), Subscription: snapshot}, nil
}
