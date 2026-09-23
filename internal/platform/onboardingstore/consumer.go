package onboardingstore

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/nats-io/nats.go"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/domain/organization"
	"safepatch/internal/platform/workqueue"
)

// Consume binds a pre-provisioned durable pull consumer; operator policy owns
// stream retention, ACLs, AckWait and delivery budgets. Workers supply an executor
// around the existing verifier, sandbox and immutable evidence store.
func (p *Proofs) Consume(ctx context.Context, connection *nats.Conn, stream, durable string, executor *GuardedExecutor) error {
	if connection == nil || executor == nil || !organization.ValidID(stream) || !organization.ValidID(durable) {
		return flow.ErrInvalid
	}
	js, err := connection.JetStream()
	if err != nil {
		return err
	}
	sub, err := js.PullSubscribe("", durable, nats.Bind(stream, durable))
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()
	for {
		messages, err := sub.Fetch(1, nats.Context(ctx))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, nats.ErrTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		for _, message := range messages {
			body, e := workqueue.Decode(message, workqueue.Proof)
			if e != nil {
				_ = message.Term()
				continue
			}
			if e = p.Run(ctx, body.OrganizationID, body.ID, "lease_"+rand.Text(), executor); e != nil {
				_ = message.Nak()
				continue
			}
			if e = message.AckSync(nats.Context(ctx)); e != nil {
				return e
			}
		}
	}
}
