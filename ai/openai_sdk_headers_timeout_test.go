package ai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// openai@7.19.0 client.js makeRequest (lines 820-834): a timeout whose fetch rejection cause has code UND_ERR_HEADERS_TIMEOUT, which undici
// raises when the dispatcher's headersTimeout (Pi: httpIdleTimeoutMs, http-dispatcher.ts) passes before the response headers, is an
// APIConnectionTimeoutError with the long headers message. The SDK's own timer abort keeps "Request timed out."
// (TestOpenAISDKTransportErrorTimeout). Probed with Pi 1.1.0, httpIdleTimeoutMs 1500 and a server that never answers: openai/gpt-4o prints
// the long message.
const wantOpenAISDKHeadersTimeout = "Request timed out. Node.js fetch timed out waiting for response headers; configure a matching undici fetch and fetchOptions.dispatcher with an Agent whose headersTimeout is at least the SDK timeout."

func TestOpenAISDKHeadersTimeoutIsTheSDKsHeadersTimeoutError(t *testing.T) {
	before := ConfiguredHTTPIdleTimeoutMs()
	t.Cleanup(func() { _ = ConfigureHTTPDispatcher(before) })
	if err := ConfigureHTTPDispatcher(200); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	for _, api := range openAISDKTransportAPIs {
		t.Run(string(api), func(t *testing.T) {
			provider := newMatrixProvider(t, &GeneratedModel{ID: "fixture", Provider: "fixture", API: api}, server.URL, false)
			defer func() { _ = provider.Close() }()
			ctx := testbudget.Context(t)
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{MaxRetries: new(0)})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonError || result.ErrorMessage != wantOpenAISDKHeadersTimeout {
				t.Fatalf("result=%q %q, want error %q", result.StopReason, result.ErrorMessage, wantOpenAISDKHeadersTimeout)
			}
		})
	}
}
