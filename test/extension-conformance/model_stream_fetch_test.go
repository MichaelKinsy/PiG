package extensionconformance

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's ProviderRequestOptions.fetch (packages/ai/src/types.ts) is the transport a model request goes through. Every SDK keeps the function
// in its process, flags it on the modelStream call, and serves the host's fetch, fetchRead and fetchClose callbacks from it: the request
// reaches the extension with its method, URL, headers and body, and the response status text and body come back, the body in bounded reads. The extension's
// function writes a status, a header and a body of its own (70000 bytes of a repeating pattern, more than one read), so an SDK that never
// sends the flag leaves the host with no client, and one that answers a constant disagrees with them.
func TestModelStreamFetchSDKsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping model stream fetch conformance in short mode (builds subprocess fixtures)")
	}
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t) // building a subprocess fixture can take minutes: the call's own deadline starts after it
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var mu sync.Mutex
			var hasClient []bool
			var status int
			var statusLine string
			var header string
			var body []byte
			var fetchErr error
			h.bridge.SetHostAction("streamModel", func(callCtx context.Context, model map[string]any, _ map[string]any) (*ai.AssistantMessageEventStream, error) {
				provider, _ := model["provider"].(string)
				modelID, _ := model["modelId"].(string)
				client := extension.ModelStreamRequestFromContext(callCtx).Fetch
				mu.Lock()
				hasClient = append(hasClient, client != nil)
				mu.Unlock()
				if client != nil {
					request, err := http.NewRequestWithContext(callCtx, http.MethodPost, "https://fetch.invalid/v1/chat?x=1", strings.NewReader("ping-body"))
					if err != nil {
						return nil, err
					}
					request.Header.Set("X-Host", "1")
					response, err := client.Do(request)
					mu.Lock()
					fetchErr = err
					mu.Unlock()
					if err == nil {
						data, readErr := io.ReadAll(response.Body)
						closeErr := response.Body.Close()
						mu.Lock()
						status, statusLine, header, body = response.StatusCode, response.Status, response.Header.Get("X-Sdk-Fetch"), data
						if fetchErr = readErr; fetchErr == nil {
							fetchErr = closeErr
						}
						mu.Unlock()
					}
				}
				return conformanceModelStream(provider, modelID)
			})
			command, ok := findCommand(h.runner, "model-stream-fetch-probe")
			if !ok {
				t.Fatal("model-stream-fetch-probe command not registered")
			}
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return slices.Contains(*h.notify, "model-fetch=ok:info") })
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(hasClient, []bool{true, false}) {
				t.Fatalf("fetch client the host saw = %v, want one with the extension's fetch and one without", hasClient)
			}
			if fetchErr != nil {
				t.Fatalf("fetch through the extension: %v", fetchErr)
			}
			want := make([]byte, 70000)
			for i := range want {
				want[i] = byte(i % 251)
			}
			if status != 207 || statusLine != "207 Answered" || header != "answered" || !bytes.Equal(body, want) {
				t.Fatalf("response = %d (%q) %q with %d bytes, want 207 (\"207 Answered\") \"answered\" with the extension's 70000-byte body", status, statusLine, header, len(body))
			}
		})
	}
}
