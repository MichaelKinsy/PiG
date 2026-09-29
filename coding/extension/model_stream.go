package extension

import (
	"context"
	"net/http"

	"github.com/MichaelKinsy/PiG/ai"
)

// ModelStreamRequest carries a resolved API-leaf request and connection-owned
// callbacks. API leaves use the caller's model/auth, not the parent catalog.
type ModelStreamRequest struct {
	API              bool
	Fetch            *http.Client
	OnPayload        func(any, *ai.Model) (any, error)
	OnResponse       func(context.Context, ai.ProviderResponse, *ai.Model) error
	TransformHeaders func(context.Context, ai.ProviderHeaders) (ai.ProviderHeaders, error)
}

type modelStreamRequestKey struct{}

// WithModelStreamRequest binds transport callbacks to one model operation.
func WithModelStreamRequest(ctx context.Context, request ModelStreamRequest) context.Context {
	return context.WithValue(ctx, modelStreamRequestKey{}, request)
}

// ModelStreamRequestFromContext returns the transport contract for this operation.
func ModelStreamRequestFromContext(ctx context.Context) ModelStreamRequest {
	request, _ := ctx.Value(modelStreamRequestKey{}).(ModelStreamRequest)
	return request
}
