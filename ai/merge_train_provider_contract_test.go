package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestMergeTrainLoginKeepsExactCancellationCause(t *testing.T) {
	for _, auth := range []*APIKeyAuth{EnvAPIKeyAuth("key", "KEY"), bedrockAuth(), vertexAuth()} {
		t.Run(auth.Name, func(t *testing.T) {
			cause := errors.New("owner cancelled login")
			ctx, cancel := context.WithCancelCause(t.Context())
			_, err := auth.Login(ctx, AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) { cancel(cause); return "ignored", nil }, Notify: func(AuthEvent) { t.Error("notification after cancellation") }})
			if err != cause { //nolint:errorlint // Provider-owned login preserves the identical abort reason, not merely a wrapped match.
				t.Fatalf("login changed cancellation reason: %v", err)
			}
		})
	}
}

// Custom fetch must not bypass source timeout options when the two transport wrappers are combined.
func TestMergeTrainCustomFetchRetainsHeaderTimeout(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages} {
		t.Run(string(api), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				called := false
				fetch := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					called = true
					<-request.Context().Done()
					return nil, request.Context().Err()
				})}
				provider := newMatrixProvider(t, &GeneratedModel{ID: "fixture", Provider: "fixture", API: api}, "https://example.invalid", false)
				start := time.Now()
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{Fetch: fetch, TimeoutMs: new(1234)})
				if api == APIAnthropicMessages {
					result := requireAnthropicSetupError(t, stream, err)
					if result.ErrorMessage != "Request timed out." {
						t.Fatalf("timeout error=%q", result.ErrorMessage)
					}
					err = errors.New(result.ErrorMessage)
				} else {
					err = awaitProviderFailure(t, stream, err)
				}
				if !called || err == nil || time.Since(start) != 1234*time.Millisecond {
					t.Fatalf("called=%v error=%v elapsed=%v", called, err, time.Since(start))
				}
			})
		})
	}
}

func TestMergeTrainCustomFetchPreservesResponseObservation(t *testing.T) {
	order := []string{}
	fetch := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		order = append(order, "fetch")
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Source": []string{"retained"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}, nil
	})}
	provider := NewOpenAIProvider(OpenAIConfig{APIKey: "key", Model: "fixture", ProviderID: "fixture", BaseURL: "https://example.invalid"})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{Fetch: fetch, OnResponse: func(_ context.Context, response ProviderResponse, _ *Model) error {
		if len(order) != 1 || order[0] != "fetch" || response.Headers["x-source"] != "retained" {
			t.Errorf("response=%#v order=%v", response, order)
		}
		order = append(order, "response")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if stream.Result().StopReason != StopReasonStop || len(order) != 2 {
		t.Fatalf("result=%#v order=%v", stream.Result(), order)
	}
}
