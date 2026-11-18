package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// D74: a direct pi-ai call carries only the identity's User-Agent and no attribution headers, as under Pi's pi-ai without an `identity` option (the bridge forwards no `identity`). headerSetServer records every request's header names and answers in the shape of api.
type headerSetServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []http.Header
}

func newHeaderSetServer(t *testing.T, api string) *headerSetServer {
	t.Helper()
	recorder := &headerSetServer{}
	recorder.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		recorder.mu.Lock()
		recorder.requests = append(recorder.requests, r.Header.Clone())
		recorder.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if api == "anthropic-messages" {
			_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"x\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"+
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"+
			"data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(recorder.Close)
	return recorder
}

func (s *headerSetServer) snapshot() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.requests...)
}

// directCallHeaderSets is the header names a direct pi-ai call of each API sends, apart from the two the HTTP transport adds (Content-Length, Accept-Encoding): the credential's own header, the API's version headers, and the identity's User-Agent. No attribution header (HTTP-Referer, X-OpenRouter-*, X-BILLING-INVOKE-ORIGIN, x-opencode-*) is among them.
var directCallHeaderSets = map[string][]string{
	"openai-completions": {"Accept", "Authorization", "Content-Type", "User-Agent"},
	"anthropic-messages": {"Accept", "Anthropic-Dangerous-Direct-Browser-Access", "Anthropic-Version", "Content-Type", "User-Agent", "X-Api-Key"},
}

func requireDirectCallHeaders(t *testing.T, api string, requests []http.Header) {
	t.Helper()
	if len(requests) != 1 {
		t.Fatalf("recorded %d provider requests, want 1", len(requests))
	}
	var names []string
	for name := range requests[0] {
		if name != "Content-Length" && name != "Accept-Encoding" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if want := directCallHeaderSets[api]; !slices.Equal(names, want) {
		t.Fatalf("%s request headers = %v, want %v", api, names, want)
	}
	if got := requests[0].Get("User-Agent"); !strings.HasPrefix(got, pigidentity.UserAgentProduct+"/") {
		t.Fatalf("User-Agent = %q, want %s/<version>", got, pigidentity.UserAgentProduct)
	}
	if piIdentity.MatchString(requests[0].Get("User-Agent")) {
		t.Fatalf("User-Agent = %q identifies the request as Pi", requests[0].Get("User-Agent"))
	}
}

// Through `pig provider-bridge` (StreamAPILeaf), for an API-key provider (Anthropic) and an OpenAI-compatible one on a custom base URL.
func TestProviderBridgeRequestCarriesOnlyTheIdentityUserAgent(t *testing.T) {
	for _, api := range []string{"openai-completions", "anthropic-messages"} {
		for _, simple := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/simple=%v", api, simple), func(t *testing.T) {
				server := newHeaderSetServer(t, api)
				run := startBridge(t)
				line, err := json.Marshal(map[string]any{
					"id": "h", "type": "stream", "simple": simple,
					"model": map[string]any{"provider": "bench", "id": "bench-1", "name": "Bench", "api": api, "baseUrl": server.URL + "/v1", "reasoning": false,
						"input": []string{"text"}, "contextWindow": 128000, "maxTokens": 4096, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}},
					"request": map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi", "timestamp": 0}}, "apiKey": "bridge-test-key"},
				})
				if err != nil {
					t.Fatal(err)
				}
				writeBridge(t, run.stdin, string(line)+"\n")
				run.until(t, "h", "result")
				requireDirectCallHeaders(t, api, server.snapshot())
			})
		}
	}
}

// Through the compat entry: a Node extension's pi-ai completeSimple reaches the host's provider with the same header set.
func TestNodeCompatCompletionRequestCarriesOnlyTheIdentityUserAgent(t *testing.T) {
	nodeDir := realNodeDir(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "node-identity-probe.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ api, provider string }{{"openai-completions", "gateway"}, {"anthropic-messages", "anthropic"}} {
		t.Run(tc.api, func(t *testing.T) {
			server := newHeaderSetServer(t, tc.api)
			home := t.TempDir()
			env := []string{
				"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"),
				"PIG_OFFLINE=1", "FORCE_COLOR=0", "PI_TELEMETRY=1", "PATH=" + nodeDir + string(os.PathListSeparator) + os.Getenv("PATH"),
			}
			p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "-e", fixture, "--no-session")
			p.send(fmt.Sprintf(`{"id":"probe","type":"prompt","message":"/probe stream %s %s/v1 %s"}`, tc.provider, server.URL, tc.api))
			p.await("probe prompt response", func(record rpcRecord) bool { return record["type"] == "response" && record["id"] == "probe" })
			requireDirectCallHeaders(t, tc.api, server.snapshot())
			p.closeAndWait("after the probe")
		})
	}
}
