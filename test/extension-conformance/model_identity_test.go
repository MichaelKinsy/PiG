package extensionconformance

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture"
)

var piIdentity = regexp.MustCompile(`(?i)(^pi$|^pi[ /(]|pi\.dev|^pi-coding-agent$)`)

// The Go, Python and Rust SDKs make no model request of their own: ModelRegistry
// Complete is one host call, and the host sends the request through the
// Services-owned runtime. A model call from each SDK therefore reaches an
// OpenRouter-, NVIDIA- or OpenCode-shaped endpoint carrying PiG's attribution
// and never Pi's (D26). The Node SDK's own paths run in the real binary in
// cmd/pig's TestNodeExtensionModelCallsCarryPiGIdentityNeverPis.
func TestSDKModelCallsCarryPiGIdentityNeverPis(t *testing.T) {
	type sdkCase struct {
		name, source string
		fused        bool
	}
	sdks := []sdkCase{
		{"go", "", true},
		{"python", "testdata/model-call-python", false},
		{"rust", "testdata/model-call-rust", false},
	}
	providers := []struct {
		id   string
		want map[string]string
	}{
		{"openrouter", map[string]string{"HTTP-Referer": pigidentity.OpenRouterReferer, "X-OpenRouter-Title": pigidentity.OpenRouterTitle, "X-OpenRouter-Categories": pigidentity.OpenRouterCategories}},
		{"nvidia", map[string]string{"X-BILLING-INVOKE-ORIGIN": pigidentity.NvidiaBillingOrigin}},
		{"opencode", map[string]string{"x-opencode-session": "sdk-session", "x-opencode-client": pigidentity.OpenCodeClient}},
	}
	for _, sdk := range sdks {
		for _, provider := range providers {
			t.Run(sdk.name+"/"+provider.id, func(t *testing.T) {
				t.Setenv("PI_TELEMETRY", "1")
				var mu sync.Mutex
				var requests []http.Header
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					mu.Lock()
					requests = append(requests, r.Header.Clone())
					mu.Unlock()
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				agentDir := t.TempDir()
				config := fmt.Sprintf(`{"providers":{%q:{"apiKey":"fixture","baseUrl":%q,"api":"openai-completions","models":[{"id":"m","name":"M"}]}}}`, provider.id, server.URL)
				if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(services.Close)
				session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "primary", Provider: ai.NewFauxProvider(ai.FauxConfig{})}, NoSession: true, SkipBuiltinTools: true})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = session.Close() }()
				host := subprocess.NewHost(t.TempDir())
				defer host.Shutdown("test done")
				bridge := subprocess.NewUIBridge(func() {})
				detach := icodingagent.WireModelOperations(bridge, icodingagent.ModelOperationBindings{CurrentModel: session.Model, ModelLookup: services.ModelRuntime().GetModel, ModelCatalog: services.ModelRuntime().GetModels, Registry: services.Registry().ModelRegistry, ModelBuilder: func(spec string) (*ai.Model, error) { return coding.BuildModel(spec, services) }, SessionHandle: session})
				defer detach()
				host.SetUIBridge(bridge)
				var loaded *extension.Extension
				if sdk.fused {
					loaded, err = host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "model-call", Enabled: true}, func(conn net.Conn) error { return testfixture.ModelCall().RunWithConn(conn) })
				} else {
					source, resolveErr := filepath.Abs(sdk.source)
					if resolveErr != nil {
						t.Fatal(resolveErr)
					}
					cfg, _, resolveErr := subprocess.ResolveExtConfigWithIdentity(source, "model-call")
					if resolveErr != nil {
						t.Fatal(resolveErr)
					}
					cfg.Isolation = "isolated"
					extensions, loadErrors := host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
					if len(loadErrors) > 0 {
						t.Fatal(loadErrors[0])
					}
					loaded = &extensions[0]
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := loaded.Commands["model-call"].Handler(t.Context(), provider.id); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				got := append([]http.Header(nil), requests...)
				mu.Unlock()
				if len(got) != 1 {
					t.Fatalf("provider received %d requests, want 1", len(got))
				}
				headers := got[0]
				for name, want := range provider.want {
					if value := headers.Get(name); value != want {
						t.Errorf("%s = %q, want %q", name, value, want)
					}
				}
				if value := headers.Get("User-Agent"); !strings.HasPrefix(value, pigidentity.UserAgentProduct+"/") {
					t.Errorf("User-Agent = %q, want %s/<version> (D65)", value, pigidentity.UserAgentProduct)
				}
				for name, values := range headers {
					for _, value := range values {
						if piIdentity.MatchString(value) {
							t.Errorf("%s = %q identifies the request as Pi", name, value)
						}
					}
				}
			})
		}
	}
}
