package subprocess_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestPortWave12CloudflareAIBinding(t *testing.T) {
	// Resolve a version-manager shim before changing HOME.
	node, err := exec.CommandContext(t.Context(), "node", "-p", "process.execPath").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(strings.TrimSpace(string(node)))+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, name := range []string{"HOME", "PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(name, t.TempDir())
	}
	// A t.TempDir name repeats the test name, which pushes the host's runtime-directory sockets past the Unix socket path limit.
	runtimeDir, err := os.MkdirTemp("", "rt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	for _, name := range []string{"PI_SESSION_FILE", "PI_SESSION_ID", "PIG_SDK_GO_ROOT"} {
		t.Setenv(name, "")
	}
	// A lost fetch carrier must fail hermetically, never attempt the real Workers endpoint.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected network request", http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(name, proxy.URL)
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	fixtures, err := filepath.Abs(filepath.Join("testdata", "port-wave-12"))
	if err != nil {
		t.Fatal(err)
	}
	for _, packed := range []bool{false, true} {
		name := "isolated"
		if packed {
			name = "packed"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
			if err != nil {
				t.Fatal(err)
			}
			session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "primary", Provider: ai.NewOpenAIProvider(ai.OpenAIConfig{Model: "primary"})}, NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
			})
			host := subprocess.NewHostWithConfigRoot(dir, filepath.Join(dir, "host"))
			t.Cleanup(func() { host.Shutdown("test done") })
			bridge := subprocess.NewUIBridge(nil)
			detach := icodingagent.WireModelOperations(bridge, icodingagent.ModelOperationBindings{
				CurrentModel: session.Model, ModelLookup: services.ModelRuntime().GetModel, ModelCatalog: services.ModelRuntime().GetModels,
				Registry: services.Registry().ModelRegistry, ModelBuilder: func(spec string) (*ai.Model, error) { return coding.BuildModel(spec, services) }, SessionHandle: session,
			})
			t.Cleanup(detach)
			host.SetUIBridge(bridge)
			cfg := subprocess.ExtConfig{Name: "binding-cases", Source: filepath.Join(fixtures, "cloudflare-ai-binding.mjs"), Enabled: true}
			var ext *extension.Extension
			if packed {
				loaded, failures := host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg, {Name: "binding-sibling", Source: filepath.Join(fixtures, "binding-sibling.mjs"), Enabled: true}})
				if len(failures) != 0 || len(loaded) != 2 {
					t.Fatalf("load packed: %v (%d extensions)", failures, len(loaded))
				}
				ext = &loaded[0]
			} else {
				ext, err = host.Load(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
			}
			command, exists := ext.Commands["binding-cases"]
			if !exists {
				t.Fatal("binding-cases was not registered")
			}
			for _, title := range []string{
				// upstream: packages/ai/test/cloudflare-ai-binding.test.ts:26
				"passes requests to the binding untouched",
				// upstream: packages/ai/test/cloudflare-ai-binding.test.ts:64
				"rejects a binding with no fetch() at construction, not on first request",
				// upstream: packages/ai/test/cloudflare-ai-binding.test.ts:70
				"keeps SDK placeholder auth off the wire when paired with null auth headers",
				// Supplemental ordering, streaming and cancellation obligations: packages/ai/src/types.ts:147-174.
				"streams response bytes only after the response observer",
				"cancels the original fetch when the request signal aborts",
			} {
				t.Run(title, func(t *testing.T) {
					if err := command.Handler(t.Context(), title); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}
