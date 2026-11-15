package ai

import (
	"context"
	"net/http"
	"sync"
)

// FailureResponse carries non-serialized HTTP status, response headers, and stream-start state for a failed assistant message.
// A request keeps it only when its context came from [WithFailureResponse]; stock requests never build one.
type FailureResponse struct {
	// Status is the HTTP status of the failed response; 0 when no response arrived.
	Status int
	// Headers are the lower-cased response headers of the failed response, when it had any.
	Headers map[string]string
	// StreamStarted reports whether the stream had emitted an event before it failed.
	StreamStarted bool
}

type failureResponseKey struct{}

// failureResponseRecorder is the response a failed HTTP request got. The context of a request that wants its failure response carries one;
// the provider notes each non-success response in it and the stream builder reads it when the request fails.
type failureResponseRecorder struct {
	mu     sync.Mutex
	status int
	header http.Header
}

// WithFailureResponse returns a context that makes a failed request made with it keep the HTTP status, response headers and stream state of
// the failure on AssistantMessage.FailureResponse, for a RetryPolicy.Decide. It is the way StreamOptions-level per-request state
// already travels (see [WithProviderMaxRetries]): a request made with any other context records nothing, so the stock failed request
// builds no FailureResponse and no header map.
func WithFailureResponse(ctx context.Context) context.Context {
	return context.WithValue(ctx, failureResponseKey{}, &failureResponseRecorder{})
}

func failureResponseRecorderOf(ctx context.Context) *failureResponseRecorder {
	recorder, _ := ctx.Value(failureResponseKey{}).(*failureResponseRecorder)
	return recorder
}

// noteFailureResponse records a non-success response for the failure the provider is about to return. A request whose context did not come
// from WithFailureResponse records nothing.
func noteFailureResponse(ctx context.Context, response *http.Response) {
	recorder := failureResponseRecorderOf(ctx)
	if recorder == nil {
		return
	}
	recorder.mu.Lock()
	recorder.status, recorder.header = response.StatusCode, response.Header
	recorder.mu.Unlock()
}

// headersOf is the recorded lower-cased headers of the response that had status; a later failure that is not that response gets none.
func (recorder *failureResponseRecorder) headersOf(status int) map[string]string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if status == 0 || recorder.status != status || len(recorder.header) == 0 {
		return nil
	}
	return headersToRecord(recorder.header)
}
