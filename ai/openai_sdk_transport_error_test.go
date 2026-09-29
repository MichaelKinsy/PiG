package ai

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// The OpenAI SDK (openai@6.40.0 client.js makeRequest) rejects a fetch failure before response headers with APIConnectionError("Connection error.") and a timeout or abort of its own timer with APIConnectionTimeoutError("Request timed out."). Pi's openai-completions, openai-responses and azure-openai-responses providers surface those messages.

var openAISDKTransportAPIs = []API{APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses}

func openAISDKStreamError(t *testing.T, provider Provider, options StreamOptions) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), options)
	if err != nil {
		return err
	}
	result := stream.Result()
	if result.StopReason != StopReasonError {
		t.Fatalf("stop reason=%s, want error: %#v", result.StopReason, result)
	}
	return errors.New(result.ErrorMessage)
}

func TestOpenAISDKTransportErrorConnectionReset(t *testing.T) {
	for _, api := range openAISDKTransportAPIs {
		t.Run(string(api), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					_ = conn.(*net.TCPConn).SetLinger(0)
					_ = conn.Close()
				}
			}()
			provider := newMatrixProvider(t, &GeneratedModel{ID: "fixture", Provider: "fixture", API: api}, "http://"+listener.Addr().String(), false)
			defer func() { _ = provider.Close() }()
			err = openAISDKStreamError(t, provider, StreamOptions{MaxRetries: new(0)})
			if err == nil || err.Error() != "Connection error." {
				t.Fatalf("error=%v, want Connection error.", err)
			}
		})
	}
}

func TestOpenAISDKTransportErrorConnectionRefused(t *testing.T) {
	for _, api := range openAISDKTransportAPIs {
		t.Run(string(api), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			_ = listener.Close()
			provider := newMatrixProvider(t, &GeneratedModel{ID: "fixture", Provider: "fixture", API: api}, "http://"+address, false)
			defer func() { _ = provider.Close() }()
			err = openAISDKStreamError(t, provider, StreamOptions{MaxRetries: new(0)})
			if err == nil || err.Error() != "Connection error." {
				t.Fatalf("error=%v, want Connection error.", err)
			}
		})
	}
}

// A listener that never accepts leaves the request waiting for headers until the per-attempt timeout fires.
func TestOpenAISDKTransportErrorTimeout(t *testing.T) {
	for _, api := range openAISDKTransportAPIs {
		t.Run(string(api), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			provider := newMatrixProvider(t, &GeneratedModel{ID: "fixture", Provider: "fixture", API: api}, "http://"+listener.Addr().String(), false)
			defer func() { _ = provider.Close() }()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			// The provider reports the failure as the stream's terminal error event, so the message is the observable contract.
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{MaxRetries: new(0), TimeoutMs: new(100)})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonError || result.ErrorMessage != "Request timed out." {
				t.Fatalf("result=%q %q, want error Request timed out.", result.StopReason, result.ErrorMessage)
			}
			if ctx.Err() != nil {
				t.Fatalf("parent ended: %v", ctx.Err())
			}
		})
	}
}

// A caller-supplied fetch that fails with a timeout-shaped error is a timeout for the SDK (`/timed? ?out/i` over the error and its cause), even before its own timer fires.
func TestOpenAISDKTransportErrorFetchTimeoutText(t *testing.T) {
	for _, api := range openAISDKTransportAPIs {
		for _, test := range []struct {
			name string
			err  error
			want string
		}{
			{"timeout", &net.OpError{Op: "dial", Net: "tcp", Err: timeoutTestError{}}, "Request timed out."},
			{"other", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("no route to host")}, "Connection error."},
		} {
			t.Run(string(api)+"/"+test.name, func(t *testing.T) {
				provider := newMatrixProvider(t, &GeneratedModel{ID: "fixture", Provider: "fixture", API: api}, "https://timeout.example.invalid", false)
				defer func() { _ = provider.Close() }()
				fetch := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, test.err })}
				err := openAISDKStreamError(t, provider, StreamOptions{MaxRetries: new(0), Fetch: fetch})
				if err == nil || err.Error() != test.want {
					t.Fatalf("error=%v, want %s", err, test.want)
				}
			})
		}
	}
}

// The SDK classifies only String(rejection) + String(rejection.cause) (client.js:369-374). Recorded from the installed Pi 0.87.1 openai@6.40.0 client with a caller-supplied fetch that throws each error:
//
//	Error("failed", {cause: Error("timed out")})                                          -> "Request timed out."
//	Error("connect timed out")                                                            -> "Request timed out."
//	Error("failed", {cause: Error("another failure", {cause: Error("timed out")})})       -> "Connection error."
//	Error("no route to host")                                                             -> "Connection error."
//	Error("dial failed") with a non-timeout code                                          -> "Connection error."
func TestOpenAISDKTransportErrorClassifiesOnlyRejectionAndImmediateCause(t *testing.T) {
	timeoutFlag := &levelError{message: "dial failed", timeout: true}
	for _, api := range openAISDKTransportAPIs {
		for _, test := range []struct {
			name string
			err  error
			want string
		}{
			{"immediate cause", &levelError{message: "failed", next: &levelError{message: "timed out"}}, "Request timed out."},
			{"rejection text", &levelError{message: "connect timed out"}, "Request timed out."},
			{"nested cause", &levelError{message: "failed", next: &levelError{message: "another failure", next: &levelError{message: "timed out"}}}, "Connection error."},
			{"plain", &levelError{message: "no route to host"}, "Connection error."},
			{"net.Error timeout flag without wording", timeoutFlag, "Connection error."},
			{"wrapped net.Error timeout flag without wording", &levelError{message: "failed", next: timeoutFlag}, "Connection error."},
		} {
			t.Run(string(api)+"/"+test.name, func(t *testing.T) {
				provider := newMatrixProvider(t, &GeneratedModel{ID: "fixture", Provider: "fixture", API: api}, "https://timeout.example.invalid", false)
				defer func() { _ = provider.Close() }()
				fetch := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, test.err })}
				err := openAISDKStreamError(t, provider, StreamOptions{MaxRetries: new(0), Fetch: fetch})
				if err == nil || err.Error() != test.want {
					t.Fatalf("error=%v, want %s", err, test.want)
				}
			})
		}
	}
}

// levelError reports only its own message from Error, like a JavaScript Error, and exposes its cause through Unwrap.
type levelError struct {
	message string
	next    error
	timeout bool
}

func (err *levelError) Error() string   { return err.message }
func (err *levelError) Unwrap() error   { return err.next }
func (err *levelError) Timeout() bool   { return err.timeout }
func (err *levelError) Temporary() bool { return false }

type timeoutTestError struct{}

func (timeoutTestError) Error() string   { return "i/o timeout" }
func (timeoutTestError) Timeout() bool   { return true }
func (timeoutTestError) Temporary() bool { return false }

// openrouter-images.ts builds an OpenAI SDK client, so its pre-header transport failures carry the same SDK messages.
func TestOpenRouterImagesSDKTransportError(t *testing.T) {
	for _, test := range []struct {
		name      string
		listen    bool
		timeoutMs int
		want      string
	}{
		{"refused", false, 0, "Connection error."},
		{"timeout", true, 100, "Request timed out."},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			if test.listen {
				defer func() { _ = listener.Close() }()
			} else {
				_ = listener.Close()
			}
			model := openRouterFluxModel()
			model.BaseURL = "http://" + address
			result, err := GenerateImages(t.Context(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: "Generate a dog"}}}, ProviderImagesOptions{APIKey: "test", TimeoutMs: test.timeoutMs})
			if err != nil {
				t.Fatal(err)
			}
			if result.StopReason != ImagesStopReasonError || result.ErrorMessage != test.want {
				t.Fatalf("result=%+v, want %s", result, test.want)
			}
		})
	}
}
