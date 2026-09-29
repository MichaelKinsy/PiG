package ai

// Ports packages/ai/src/api/openai-completions.ts
// Ports packages/ai/src/api/openai-responses.ts
// Ports packages/ai/src/api/anthropic-messages.ts

import (
	"context"
	"io"
	"net/http"
	"time"
)

type providerRequestOptionsKey struct{}

type providerRequestTimeoutError struct{}

func (*providerRequestTimeoutError) Error() string { return context.DeadlineExceeded.Error() }
func (*providerRequestTimeoutError) Unwrap() error { return context.DeadlineExceeded }

func withProviderRequestOptions(ctx context.Context, options StreamOptions) context.Context {
	if options.MaxRetries != nil {
		ctx = WithProviderMaxRetries(ctx, *options.MaxRetries)
	}
	if options.MaxRetryDelayMs != nil {
		ctx = WithProviderRequestRetry(ctx, ProviderMaxRetries(ctx), options.MaxRetryDelayMs)
	}
	return context.WithValue(ctx, providerRequestOptionsKey{}, options)
}

// providerRequestTransport owns the SDK timeout for one header-establishment attempt. Like OpenAI/Anthropic fetchWithTimeout, receipt of response headers stops that timer; body lifetime remains owned by the request and body reader.
type providerRequestTransport struct{ base http.RoundTripper }

func (t *providerRequestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	options, _ := request.Context().Value(providerRequestOptionsKey{}).(StreamOptions)
	if options.TimeoutMs == nil {
		return t.base.RoundTrip(request)
	}
	ctx, cancel := context.WithCancelCause(request.Context())
	delay := time.Duration(*options.TimeoutMs) * time.Millisecond
	if *options.TimeoutMs == 0 {
		// Node's setTimeout keeps an explicit zero armed and schedules it after one millisecond.
		delay = time.Millisecond
	}
	timer := time.AfterFunc(delay, func() { cancel(&providerRequestTimeoutError{}) })
	response, err := t.base.RoundTrip(request.WithContext(ctx))
	timer.Stop()
	if err != nil {
		cause := context.Cause(ctx)
		cancel(nil)
		if cause != nil {
			return nil, cause
		}
		return nil, err
	}
	response.Body = &providerResponseBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}
func (t *providerRequestTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type providerResponseBody struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
}

func (b *providerResponseBody) Close() error { err := b.ReadCloser.Close(); b.cancel(nil); return err }
