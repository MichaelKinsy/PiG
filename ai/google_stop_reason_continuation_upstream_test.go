//go:build !pig_strip_google_vertex

package ai

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// Ports packages/ai/test/google-stop-reason-continuation.test.ts (Pi f72f799a4, #12103; the file is new with that commit). Case :30 "maps CONTINUATION
// to length, like MAX_TOKENS" ran red on mapStopReason and passes with its CONTINUATION arm (google-shared.ts:506). Case :35 "ends the turn as length
// instead of letting the SDK send a continuation request": Pi turns the SDK's automatic continuation off (`automaticContinuation: false`); the Go
// adapters have no SDK and never resume a stream, so the request count (2, one per adapter) held before the change and the stop reason needed the mapping.
func TestGoogleContinuationMapsToLengthLikeMaxTokens(t *testing.T) {
	got, err := mapStopReason("CONTINUATION")
	if err != nil || got != StopReasonLength {
		t.Fatalf("mapStopReason(CONTINUATION) = (%q, %v), want length", got, err)
	}
}

func TestGoogleContinuationEndsTheTurnAsLengthInsteadOfSendingAContinuationRequest(t *testing.T) {
	chunk := `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"CONTINUATION","continuationToken":"token-1"}]}`
	var calls atomic.Int32
	fetch := &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + chunk + "\n\n")), Request: request}, nil
	})}
	for _, api := range []API{APIGoogleGenerativeAI, APIGoogleVertex} {
		stream, err := fetchOptionProvider(api, "https://upstream.test/v1").Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}}), StreamOptions{Fetch: fetch, Transport: TransportSSE})
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonLength {
			t.Errorf("%s stop reason = %q (%q), want length", api, result.StopReason, result.ErrorMessage)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("fetch calls = %d, want 2", calls.Load())
	}
}
