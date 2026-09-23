package replayadapter

import (
	"context"
	"go.opentelemetry.io/otel/baggage"
)

func withoutBaggage(ctx context.Context) context.Context { return baggage.ContextWithoutBaggage(ctx) }
