package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Port of Pi main 8a57d42a5de8f3f87ebd0cb40c8ece4aed0fc336 (#12502, #12376).
func TestProviderErrorResponsesReportedToOnResponse(t *testing.T) {
	ctxMessages := NormalizeContext(Context{
		Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}},
	})

	adapters := []struct {
		name        string
		headers     bool
		newProvider func(baseURL string) Provider
	}{
		{
			name:    "openai-completions",
			headers: true,
			newProvider: func(baseURL string) Provider {
				return NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test", BaseURL: baseURL})
			},
		},
		{
			name:    "openai-responses",
			headers: true,
			newProvider: func(baseURL string) Provider {
				return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test", BaseURL: baseURL})
			},
		},
		{
			name:    "azure-openai-responses",
			headers: true,
			newProvider: func(baseURL string) Provider {
				return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test", BaseURL: baseURL})
			},
		},
		{
			name:    "anthropic-messages",
			headers: true,
			newProvider: func(baseURL string) Provider {
				return NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test", BaseURL: baseURL})
			},
		},
		{
			name:    "google-generative-ai",
			headers: false,
			newProvider: func(baseURL string) Provider {
				return NewGoogleProvider(GoogleConfig{APIKey: "test-key", Model: "test-model", ProviderID: "test", BaseURL: baseURL})
			},
		},
	}

	for _, adapter := range adapters {
		t.Run(adapter.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "0")
				w.Header().Set("X-Request-Id", "req-1")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error","code":429}}`))
			}))
			defer server.Close()

			var responses []ProviderResponse
			provider := adapter.newProvider(server.URL)
			stream, err := provider.Stream(t.Context(), ctxMessages, StreamOptions{
				MaxRetries: new(1),
				OnResponse: func(_ context.Context, resp ProviderResponse, _ *Model) error {
					responses = append(responses, resp)
					return nil
				},
			})
			if err == nil {
				res := stream.Result()
				if res.StopReason != StopReasonError {
					t.Fatalf("stopReason = %q, want error", res.StopReason)
				}
			}

			if got := requests.Load(); got != 2 {
				t.Fatalf("requests = %d, want 2", got)
			}
			if len(responses) != 2 || responses[0].Status != 429 || responses[1].Status != 429 {
				t.Fatalf("responses = %#v, want two 429s", responses)
			}
			if adapter.headers {
				if responses[0].Headers["retry-after"] != "0" || responses[0].Headers["x-request-id"] != "req-1" {
					t.Fatalf("headers = %#v, want retry-after=0 x-request-id=req-1", responses[0].Headers)
				}
			} else {
				if len(responses[0].Headers) != 0 {
					t.Fatalf("google headers = %#v, want empty", responses[0].Headers)
				}
			}
		})
	}
}

type stubProviderError struct {
	status  *int
	headers http.Header
	msg     string
}

func (e stubProviderError) Error() string                { return e.msg }
func (e stubProviderError) ProviderStatus() *int         { return e.status }
func (e stubProviderError) ProviderHeaders() http.Header { return e.headers }

func TestRetryProviderRequestErrorResponses(t *testing.T) {
	t.Run("reports each failed attempt status and headers before retrying", func(t *testing.T) {
		var responses []ProviderResponse
		calls := 0
		res, err := RetryProviderRequest(t.Context(), func() (string, error) {
			calls++
			switch calls {
			case 1:
				return "", stubProviderError{status: new(429), headers: http.Header{"Retry-After-Ms": {"0"}}, msg: "429"}
			case 2:
				return "", stubProviderError{status: new(503), headers: http.Header{"Retry-After-Ms": {"0"}}, msg: "503"}
			default:
				return "ok", nil
			}
		}, ProviderRetryOptions{
			MaxRetries: new(2),
			OnErrorResponse: func(r ProviderResponse) error {
				responses = append(responses, r)
				return nil
			},
		})
		if err != nil || res != "ok" {
			t.Fatalf("res=%q err=%v", res, err)
		}
		if len(responses) != 2 || responses[0].Status != 429 || responses[1].Status != 503 {
			t.Fatalf("responses = %#v", responses)
		}
	})

	t.Run("reports final non-retryable failure", func(t *testing.T) {
		var responses []ProviderResponse
		wantErr := stubProviderError{status: new(400), headers: http.Header{"X-Request-Id": {"req-1"}}, msg: "400"}
		_, err := RetryProviderRequest(t.Context(), func() (string, error) {
			return "", wantErr
		}, ProviderRetryOptions{
			MaxRetries: new(2),
			OnErrorResponse: func(r ProviderResponse) error {
				responses = append(responses, r)
				return nil
			},
		})
		if err == nil || err.Error() != wantErr.Error() {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
		if len(responses) != 1 || responses[0].Status != 400 || responses[0].Headers["x-request-id"] != "req-1" {
			t.Fatalf("responses = %#v", responses)
		}
	})

	t.Run("does not report failures without HTTP status", func(t *testing.T) {
		called := false
		_, err := RetryProviderRequest(t.Context(), func() (string, error) {
			return "", context.DeadlineExceeded
		}, ProviderRetryOptions{
			OnErrorResponse: func(ProviderResponse) error {
				called = true
				return nil
			},
		})
		if err == nil || called {
			t.Fatalf("err=%v called=%v", err, called)
		}
	})

	t.Run("fails immediately when callback returns error", func(t *testing.T) {
		calls := 0
		cbErr := context.Canceled
		_, err := RetryProviderRequest(t.Context(), func() (string, error) {
			calls++
			return "", stubProviderError{status: new(429), headers: http.Header{"Retry-After-Ms": {"0"}}, msg: "429"}
		}, ProviderRetryOptions{
			MaxRetries:      new(2),
			OnErrorResponse: func(ProviderResponse) error { return cbErr },
		})
		if !errors.Is(err, cbErr) || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	})

	t.Run("does not report failure after context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		called := false
		_, err := RetryProviderRequest(ctx, func() (string, error) {
			cancel()
			return "", stubProviderError{status: new(429), msg: "429"}
		}, ProviderRetryOptions{
			OnErrorResponse: func(ProviderResponse) error {
				called = true
				return nil
			},
		})
		if err == nil || called {
			t.Fatalf("err=%v called=%v", err, called)
		}
	})
}

func TestRetryGoogleRequestReportsErrorResponseWithEmptyHeaders(t *testing.T) {
	var responses []ProviderResponse
	_, err := RetryGoogleRequest(t.Context(), func() (string, error) {
		return "", &providerError{status: new(429), message: "got status: 429"}
	}, StreamOptions{
		OnResponse: func(_ context.Context, r ProviderResponse, _ *Model) error {
			responses = append(responses, r)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(responses) != 1 || responses[0].Status != 429 || len(responses[0].Headers) != 0 {
		t.Fatalf("responses = %#v, want single 429 with empty headers", responses)
	}
}

type failingBodyRoundTripper struct {
	status  int
	headers http.Header
}

func (rt failingBodyRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: rt.status,
		Status:     http.StatusText(rt.status),
		Header:     rt.headers,
		Body:       io.NopCloser(errReader{err: errors.New("body stream failed")}),
	}, nil
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestPostClassifierRequestReportsErrorResponseBeforeBodyFails(t *testing.T) {
	var responses []ProviderResponse
	client := &http.Client{Transport: failingBodyRoundTripper{
		status:  503,
		headers: http.Header{"Retry-After": {"7"}},
	}}
	model := ClassifierModel{ID: "test-cls", Provider: "radius", BaseURL: "https://api.example.test"}
	_, err := PostClassifierRequest(t.Context(), "Pi Classifier", "https://api.example.test/classify", model, map[string]any{"x": 1}, ClassifierOptions{
		APIKey:     "radius-key",
		MaxRetries: new(0),
		Fetch:      client,
		OnResponse: func(r ProviderResponse, _ ClassifierModel) error {
			responses = append(responses, r)
			return nil
		},
	}, nil)
	if err == nil {
		t.Fatal("expected error from failing body")
	}
	if len(responses) != 1 || responses[0].Status != 503 || responses[0].Headers["retry-after"] != "7" {
		t.Fatalf("responses = %#v, want 503 with retry-after=7", responses)
	}
}
