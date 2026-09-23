package replayadapter

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"safepatch/internal/replay"
)

type Observer struct{ tracer trace.Tracer }

func NewObserver(provider trace.TracerProvider) (*Observer, error) {
	if provider == nil {
		return nil, replay.ErrInvalid
	}
	return &Observer{provider.Tracer("safepatch/replay")}, nil
}
func (o *Observer) Start(ctx context.Context, scope replay.Scope, run, record string, source replay.TraceLink) (context.Context, func(replay.Decision)) {
	options := []trace.SpanStartOption{trace.WithNewRoot(), trace.WithAttributes(attribute.String("safepatch.organization_id", scope.OrganizationID), attribute.String("safepatch.repository_id", scope.RepositoryID), attribute.String("safepatch.replay.run_id", run), attribute.String("safepatch.replay.record_id", record))}
	if source.Validate() == nil && source.TraceID != "" {
		tid, _ := trace.TraceIDFromHex(source.TraceID)
		sid, _ := trace.SpanIDFromHex(source.SpanID)
		options = append(options, trace.WithLinks(trace.Link{SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, Remote: true})}))
	}
	// NewRoot avoids joining an untrusted captured production trace. Baggage is
	// deliberately discarded; the returned context keeps cancellation/deadlines.
	ctx = withoutBaggage(ctx)
	ctx, span := o.tracer.Start(ctx, "safepatch.replay.case", options...)
	return ctx, func(d replay.Decision) {
		span.SetAttributes(attribute.String("safepatch.replay.decision", string(d)))
		if d == replay.Unknown {
			span.SetStatus(codes.Error, "replay incomplete")
		}
		span.End()
	}
}
