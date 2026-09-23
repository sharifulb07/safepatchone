package onboardingstore

import (
	"context"
	"github.com/nats-io/nats.go"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/domain/organization"
	"safepatch/internal/platform/workqueue"
	"time"
)

type JetStreamPublisher struct {
	js     nats.JetStreamContext
	stream string
}

func NewJetStreamPublisher(connection *nats.Conn, stream string) (*JetStreamPublisher, error) {
	if connection == nil || !organization.ValidID(stream) {
		return nil, flow.ErrInvalid
	}
	js, err := connection.JetStream(nats.MaxWait(10 * time.Second))
	if err != nil {
		return nil, err
	}
	return &JetStreamPublisher{js, stream}, nil
}
func (p *JetStreamPublisher) Publish(ctx context.Context, org, id string) error {
	if !organization.ValidID(org) || !organization.ValidID(id) {
		return flow.ErrInvalid
	}
	if p == nil || p.js == nil || ctx == nil {
		return flow.ErrInvalid
	}
	m, err := workqueue.Encode(workqueue.Proof, org, id, p.stream)
	if err != nil {
		return err
	}
	_, err = p.js.PublishMsg(m, nats.Context(ctx))
	return err
}
