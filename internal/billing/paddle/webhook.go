package paddle

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/jsonobject"
	"strconv"
	"strings"
	"time"
)

type event struct {
	ID         string    `json:"event_id"`
	Type       string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
	Data       struct {
		ID             string `json:"id"`
		SubscriptionID string `json:"subscription_id"`
	} `json:"data"`
}
type verifiedEvent struct {
	adapter  *Adapter
	event    event
	signedAt time.Time
}

func (*verifiedEvent) Provider() string { return Provider }
func (a *Adapter) fresh(t time.Time) bool {
	return !t.Before(a.now().Add(-SignatureTolerance)) && !t.After(a.now().Add(SignatureTolerance))
}
func (a *Adapter) VerifyWebhook(ctx context.Context, d billing.WebhookDelivery) (billing.VerifiedWebhookEvent, error) {
	if ctx == nil || a == nil {
		return nil, billing.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(d.Payload) == 0 || len(d.Payload) > MaxPayloadBytes || len(d.Signature) > 4096 {
		return nil, billing.ErrUnverifiedEvent
	}
	stamp := ""
	var signatures [][]byte
	for _, part := range strings.Split(d.Signature, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, billing.ErrUnverifiedEvent
		}
		switch k {
		case "ts":
			if stamp != "" {
				return nil, billing.ErrUnverifiedEvent
			}
			stamp = v
		case "h1":
			b, e := hex.DecodeString(v)
			if e != nil || len(b) != 32 || len(signatures) >= 8 {
				return nil, billing.ErrUnverifiedEvent
			}
			signatures = append(signatures, b)
		}
	}
	ts, e := strconv.ParseInt(stamp, 10, 64)
	at := time.Unix(ts, 0).UTC()
	if e != nil || ts <= 0 || strconv.FormatInt(ts, 10) != stamp || !a.fresh(at) || len(signatures) == 0 {
		return nil, billing.ErrUnverifiedEvent
	}
	valid := false
	for _, secret := range a.secrets {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(stamp + ":"))
		m.Write(d.Payload)
		for _, sig := range signatures {
			if hmac.Equal(m.Sum(nil), sig) {
				valid = true
			}
		}
	}
	if !valid {
		return nil, billing.ErrUnverifiedEvent
	}
	raw, e := jsonobject.Normalize(d.Payload, MaxPayloadBytes)
	var v event
	if e != nil || json.Unmarshal(raw, &v) != nil || !validID(v.ID, "evt_") || v.OccurredAt.IsZero() || v.OccurredAt.After(at.Add(SignatureTolerance)) {
		return nil, billing.ErrInvalidData
	}
	return &verifiedEvent{a, v, at}, nil
}
func (a *Adapter) SubscriptionNotification(ctx context.Context, h billing.VerifiedWebhookEvent) (billing.Notification, error) {
	fail := billing.Notification{}
	v, ok := h.(*verifiedEvent)
	if !ok || v == nil || v.adapter != a || !a.fresh(v.signedAt) {
		return fail, billing.ErrUnverifiedEvent
	}
	id := ""
	switch v.event.Type {
	case "subscription.created", "subscription.activated", "subscription.updated", "subscription.canceled", "subscription.paused", "subscription.resumed", "subscription.past_due":
		id = v.event.Data.ID
	case "transaction.completed", "transaction.payment_failed", "transaction.past_due":
		id = v.event.Data.SubscriptionID
		if id == "" {
			return fail, billing.ErrUnsupportedEvent
		}
	default:
		return fail, billing.ErrUnsupportedEvent
	}
	s, err := a.getSubscription(ctx, id)
	if err != nil {
		return fail, err
	}
	i, err := a.establish(ctx, s)
	if err != nil {
		return fail, err
	}
	n := billing.Notification{Provider: Provider, EventID: v.event.ID, OrganizationID: i.OrganizationID, ExternalReference: s.ID, OccurredAt: v.event.OccurredAt}
	return n, n.Validate()
}
func (a *Adapter) ProcessVerifiedWebhookEvent(ctx context.Context, h billing.VerifiedWebhookEvent) (billing.SubscriptionEvent, error) {
	n, err := a.SubscriptionNotification(ctx, h)
	if err != nil {
		return billing.SubscriptionEvent{}, err
	}
	s, err := a.ReadSubscription(ctx, billing.SubscriptionRequest{OrganizationID: n.OrganizationID, ExternalReference: n.ExternalReference})
	if err != nil {
		return billing.SubscriptionEvent{}, err
	}
	return billing.SubscriptionEvent{Provider: Provider, EventID: n.EventID, OccurredAt: n.OccurredAt, Subscription: s}, nil
}

type Integration struct {
	adapter *Adapter
	store   billing.SubscriptionStore
}

func NewIntegration(a *Adapter, s billing.SubscriptionStore) (*Integration, error) {
	if a == nil || s == nil {
		return nil, billing.ErrInvalidConfiguration
	}
	return &Integration{a, s}, nil
}
func (s *Integration) Handle(ctx context.Context, d billing.WebhookDelivery) error {
	h, err := s.adapter.VerifyWebhook(ctx, d)
	if err != nil {
		return err
	}
	n, err := s.adapter.SubscriptionNotification(ctx, h)
	if err != nil {
		return err
	}
	if err = s.store.Accept(ctx, n); err != nil {
		return err
	}
	_, err = s.store.Reconcile(ctx, n.EventID, s.adapter)
	return err
}
