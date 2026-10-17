package cli

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// piIdentity matches values Pi sends to identify itself: pi.dev, a bare "pi"
// or "Pi", pi-coding-agent, and pi/<version> or "pi (<platform>...)" user agents.
var piIdentity = regexp.MustCompile(`(?i)(^pi$|^pi[ /(]|pi\.dev|^pi-coding-agent$)`)

type identityRecorder struct {
	mu       sync.Mutex
	requests []http.Header
}

func newIdentityRecorder(t *testing.T) (*identityRecorder, *httptest.Server) {
	t.Helper()
	recorder := &identityRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		recorder.mu.Lock()
		recorder.requests = append(recorder.requests, r.Header.Clone())
		recorder.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"+
			"data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return recorder, server
}

func (r *identityRecorder) snapshot() []http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]http.Header(nil), r.requests...)
}

// realNodeDir names the directory of the node executable itself: the test
// runs with a temporary HOME, under which a version-manager shim cannot resolve.
func realNodeDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("node", "-p", "process.execPath").Output()
	if err != nil {
		t.Fatalf("node is required for the Node extension runtime: %v", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

// A Node extension can make its own model calls: the vendored SDK's
// createAgentSession/prompt (Pi's core/sdk.ts attribution wiring) and pi-ai's
// completeSimple (D74's host bridge). Run through the real binary against a
// recording server shaped like OpenRouter and OpenCode (Pi selects those by
// provider id or base URL: provider-attribution.ts isOpenRouterModel,
// getSessionHeaders), every request carries PiG's attribution and none of Pi's
// (D26).
func TestNodeExtensionModelCallsCarryPiGIdentityNeverPis(t *testing.T) {
	type shape struct {
		name, provider, path string
		want                 func(sessionID string) map[string]string
	}
	shapes := []shape{
		{"openrouter", "openrouter", "/api/v1", func(string) map[string]string {
			return map[string]string{"HTTP-Referer": pigidentity.OpenRouterReferer, "X-OpenRouter-Title": pigidentity.OpenRouterTitle, "X-OpenRouter-Categories": pigidentity.OpenRouterCategories}
		}},
		// Pi's isOpenRouterModel also matches any base URL containing openrouter.ai.
		{"openrouter-base-url", "gateway", "/proxy/openrouter.ai/v1", func(string) map[string]string {
			return map[string]string{"HTTP-Referer": pigidentity.OpenRouterReferer, "X-OpenRouter-Title": pigidentity.OpenRouterTitle, "X-OpenRouter-Categories": pigidentity.OpenRouterCategories}
		}},
		{"nvidia", "nvidia", "/v1", func(string) map[string]string {
			return map[string]string{"X-BILLING-INVOKE-ORIGIN": pigidentity.NvidiaBillingOrigin}
		}},
		{"opencode", "opencode", "/zen/v1", func(sessionID string) map[string]string {
			return map[string]string{"x-opencode-session": sessionID, "x-opencode-client": pigidentity.OpenCodeClient}
		}},
	}
	nodeDir := realNodeDir(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "node-identity-probe.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"session", "stream"} {
		for _, shape := range shapes {
			t.Run(mode+"/"+shape.name, func(t *testing.T) {
				recorder, server := newIdentityRecorder(t)
				home := t.TempDir()
				env := []string{
					"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"),
					"PIG_OFFLINE=1", "FORCE_COLOR=0", "PI_TELEMETRY=1", "PATH=" + nodeDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				}
				p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "-e", fixture, "--no-session")
				id := "probe-" + shape.name
				p.send(fmt.Sprintf(`{"id":%q,"type":"prompt","message":"/probe %s %s %s%s"}`, id, mode, shape.provider, server.URL, shape.path))
				p.await("probe prompt response", func(record rpcRecord) bool { return record["type"] == "response" && record["id"] == id })
				requests := recorder.snapshot()
				if len(requests) != 1 {
					t.Fatalf("recorded %d model requests, want 1\n%s", len(requests), p.stderr.String())
				}
				headers := requests[0]
				sessionID := headers.Get("x-opencode-session")
				if shape.name == "opencode" && sessionID == "" {
					t.Fatalf("the OpenCode session header is missing: %v", headers)
				}
				for name, want := range shape.want(sessionID) {
					if got := headers.Get(name); got != want {
						t.Errorf("%s = %q, want %q", name, got, want)
					}
				}
				if got := headers.Get("User-Agent"); !strings.HasPrefix(got, pigidentity.UserAgentProduct+"/") {
					t.Errorf("User-Agent = %q, want %s/<version> (D65)", got, pigidentity.UserAgentProduct)
				}
				for name, values := range headers {
					for _, value := range values {
						if piIdentity.MatchString(value) {
							t.Errorf("%s = %q identifies the request as Pi", name, value)
						}
					}
				}
				p.closeAndWait("after the probe")
			})
		}
	}
}
