package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func newModelRegistryTestSession(t *testing.T, services *coding.AgentSessionServices) *coding.Session {
	t.Helper()
	model, err := coding.BuildModel("openai/gpt-5.4", services)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := coding.NewRuntime(coding.RuntimeOptions{Services: services})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	session, err := runtime.New(coding.SessionStartOptions{Model: model, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestModelOperationsThroughPrintJSONAndRPCEntrypoints(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "model-operations-entrypoint.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			config := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(home, "model-operations.marker")
			args := []string{"--model", "test-faux/faux-1", "--no-session", "-e", fixture}
			switch mode {
			case "print":
				args = append(args, "--print", "What is 20+22?")
			case "json":
				args = append(args, "--mode", "json", "--print", "What is 20+22?")
			case "rpc":
				args = append(args, "--mode", "rpc")
			}
			ctx := testbudget.Context(t)
			command := exec.CommandContext(ctx, binary, args...)
			command.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_MODEL_OPERATIONS_MARKER="+marker, "PIG_QUIET_STARTUP=1")
			if mode == "rpc" {
				command.Stdin = strings.NewReader("{\"id\":\"prompt\",\"type\":\"prompt\",\"message\":\"What is 20+22?\"}\n")
			}
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			if err := command.Run(); err != nil {
				t.Fatalf("%s entrypoint: %v\n%s", mode, err, output.String())
			}
			observed, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("%s marker: %v\n%s", mode, err, output.String())
			}
			if string(observed) != mode {
				t.Fatalf("%s extension mode = %q", mode, observed)
			}
		})
	}
}

func TestSharedSubprocessModelOperationsAcrossModes(t *testing.T) {
	for _, mode := range []extension.ExtensionMode{extension.ModePrint, extension.ModeJSON, extension.ModeRPC} {
		t.Run(string(mode), func(t *testing.T) {
			t.Setenv("PIG_TEST_FAUX", "1")
			t.Setenv("PIG_TEST_FAUX_SCENARIO", "parity-basic")
			agentDir := t.TempDir()
			config := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			model, err := coding.BuildModel("test-faux/faux-1", services)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := coding.NewRuntime(coding.RuntimeOptions{Services: services})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = runtime.Close() }()
			session, err := runtime.New(coding.SessionStartOptions{Model: model, NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			bridge := subprocess.NewUIBridge(func() {})
			host := subprocess.NewHost(t.TempDir())
			host.SetMode(string(mode))
			host.SetUIBridge(bridge)
			defer host.Shutdown("test done")
			detach := wireSubprocessModelRegistry(bridge, session, services)
			defer detach()
			fixture, err := filepath.Abs(filepath.Join("testdata", "model-operations.mjs"))
			if err != nil {
				t.Fatal(err)
			}
			ctx := testbudget.Context(t)
			ext, err := host.Load(ctx, subprocess.ExtConfig{Name: "model-operations", Source: fixture, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			command, ok := ext.Commands["model_operations"]
			if !ok {
				t.Fatal("model_operations command not registered")
			}
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// piModelRegistryObjects runs a model registry program through the installed Pi 1.2.0's ModelRuntime, the runtime behind an
// extension's ctx.modelRegistry (testdata/model-registry-oracle.mjs), and returns, for each find step, JSON.stringify of each
// model object Pi holds: what an extension reads from ctx.modelRegistry.find after the same steps. A model Pi does not have is nil.
func piModelRegistryObjects(t *testing.T, program ...map[string]any) []map[string]*string {
	t.Helper()
	root, err := filepath.EvalSymlinks("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Version != pigversion.UpstreamPackageVersion {
		t.Fatalf("installed Pi %q (%v), want %s", manifest.Version, err, pigversion.UpstreamPackageVersion)
	}
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	command := exec.CommandContext(testbudget.Context(t), "node", filepath.Join("testdata", "model-registry-oracle.mjs"), root, string(encoded))
	command.Env = append(os.Environ(), "HOME="+home, "PI_OFFLINE=1", "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Pi model registry oracle: %v\n%s", err, stderr.String())
	}
	var finds []map[string]*string
	if err := json.Unmarshal(output, &finds); err != nil {
		t.Fatalf("Pi model registry oracle output %q: %v", output, err)
	}
	return finds
}

// modelRegistryCheck is the model_registry_refresh argument (testdata/model-registry-refresh.mjs): the model objects Pi holds for
// provider ("" is registry-refresh) and, when auth is not nil, the auth every present model resolves.
func modelRegistryCheck(t *testing.T, provider string, objects map[string]*string, auth any) string {
	t.Helper()
	check := map[string]any{"objects": objects}
	if provider != "" {
		check["provider"] = provider
	}
	if auth != nil {
		check["auth"] = auth
	}
	data, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSubprocessModelRegistryRefreshesConnectedNode(t *testing.T) {
	agentDir := t.TempDir()
	modelsPath := filepath.Join(agentDir, "models.json")
	writeModels := func(data string) {
		t.Helper()
		if err := os.WriteFile(modelsPath, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first := `{"providers":{"registry-refresh":{"baseUrl":"https://models.invalid/v1","api":"openai-completions","authHeader":false,"models":[{"id":"remove-me","name":"Remove me"},{"id":"org/model/name","name":"Slash model"}],"modelOverrides":{"org/model/name":{"name":"First override"},"override-only":{"name":"Must stay absent"}}}}}`
	second := `{"providers":{"registry-refresh":{"baseUrl":"https://models.invalid/v1","api":"openai-completions","authHeader":false,"models":[{"id":"added","name":"Added"},{"id":"org/model/name","name":"Slash model"}],"modelOverrides":{"org/model/name":{"name":"Second override"},"override-only":{"name":"Must stay absent"}}}}}`
	find := map[string]any{"find": []string{"registry-refresh", "remove-me", "org/model/name", "added", "override-only"}}
	want := piModelRegistryObjects(t, map[string]any{"modelsJson": first}, find, map[string]any{"modelsJson": second}, find)
	writeModels(first)

	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	bridge := subprocess.NewUIBridge(func() {})
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	session := newModelRegistryTestSession(t, services)
	detachModelRegistry := wireSubprocessModelRegistry(bridge, session, services)
	defer detachModelRegistry()

	fixture, err := filepath.Abs(filepath.Join("testdata", "model-registry-refresh.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := testbudget.Context(t)
	ext, err := host.Load(ctx, subprocess.ExtConfig{Name: "model-registry-refresh", Source: fixture, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	command, ok := ext.Commands["model_registry_refresh"]
	if !ok {
		t.Fatal("model_registry_refresh command not registered")
	}
	if err := command.Handler(ctx, modelRegistryCheck(t, "", want[0], nil)); err != nil {
		t.Fatal(err)
	}

	writeModels(second)
	services.Registry().ModelRegistry.Refresh()

	if model := services.ModelRuntime().GetModel("registry-refresh", "remove-me"); model != nil {
		t.Fatalf("direct lookup retained removed model: %#v", model)
	}
	if model := services.ModelRuntime().GetModel("registry-refresh", "added"); model == nil || model.DisplayName != "Added" {
		t.Fatalf("direct added model = %#v", model)
	}
	if model := services.ModelRuntime().GetModel("registry-refresh", "org/model/name"); model == nil || model.DisplayName != "Second override" {
		t.Fatalf("direct override model = %#v", model)
	}
	if model := services.ModelRuntime().GetModel("registry-refresh", "override-only"); model != nil {
		t.Fatalf("direct lookup fabricated override-only model: %#v", model)
	}
	if err := command.Handler(ctx, modelRegistryCheck(t, "", want[1], nil)); err != nil {
		t.Fatal(err)
	}
}

func TestSubprocessModelRegistryPublishesProviderOnlyOverlayRefresh(t *testing.T) {
	agentDir := t.TempDir()
	modelsPath := filepath.Join(agentDir, "models.json")
	config := func(baseURL string, strict bool, version string) string {
		return fmt.Sprintf(`{"providers":{"cloudflare-ai-gateway":{"baseUrl":%q,"headers":{"X-Version":%q},"compat":{"supportsStrictMode":%t}}}}`, baseURL, version, strict)
	}
	writeModels := func(data string) {
		t.Helper()
		if err := os.WriteFile(modelsPath, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first, second := config("https://first.invalid/v1", false, "first"), config("https://second.invalid/v1", true, "second")
	find := map[string]any{"find": []string{"cloudflare-ai-gateway", "gpt-5.6-luna"}}
	want := piModelRegistryObjects(t, map[string]any{"modelsJson": first}, find, map[string]any{"modelsJson": second}, find)
	writeModels(first)
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	bridge := subprocess.NewUIBridge(func() {})
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	session := newModelRegistryTestSession(t, services)
	detachModelRegistry := wireSubprocessModelRegistry(bridge, session, services)
	defer detachModelRegistry()
	fixture, err := filepath.Abs(filepath.Join("testdata", "model-registry-refresh.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := testbudget.Context(t)
	ext, err := host.Load(ctx, subprocess.ExtConfig{Name: "model-registry-refresh", Source: fixture, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	command, ok := ext.Commands["model_registry_refresh"]
	if !ok {
		t.Fatal("model_registry_refresh command not registered")
	}
	if err := command.Handler(ctx, modelRegistryCheck(t, "cloudflare-ai-gateway", want[0], map[string]any{"headers": map[string]string{"X-Version": "first"}})); err != nil {
		t.Fatal(err)
	}
	writeModels(second)
	services.Registry().ModelRegistry.Refresh()
	if err := command.Handler(ctx, modelRegistryCheck(t, "cloudflare-ai-gateway", want[1], map[string]any{"headers": map[string]string{"X-Version": "second"}})); err != nil {
		t.Fatal(err)
	}
}

func TestSubprocessModelRegistryPublishesExtensionReplacementAndRestoration(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"openai":{"modelOverrides":{"extension-only":{"name":"Configured extension","reasoning":false,"cost":{"input":0}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	replacement := extension.ProviderConfig{BaseURL: "https://extension.invalid/v1", API: ai.APIOpenAIResponses, Models: []extension.ProviderModelConfig{{ID: "extension-only", Name: "Extension name", API: ai.APIOpenAIResponses, Reasoning: true, Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100, Cost: extension.ProviderModelCost{Input: 9}}}}
	find := map[string]any{"find": []string{"openai", "gpt-5.4", "extension-only"}}
	want := piModelRegistryObjects(t, map[string]any{"modelsJson": config}, find, map[string]any{"register": []any{"openai", replacement}}, find, map[string]any{"unregister": "openai"}, find)
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	bridge := subprocess.NewUIBridge(func() {})
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	session := newModelRegistryTestSession(t, services)
	detachModelRegistry := wireSubprocessModelRegistry(bridge, session, services)
	defer detachModelRegistry()
	fixture, err := filepath.Abs(filepath.Join("testdata", "model-registry-refresh.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := testbudget.Context(t)
	ext, err := host.Load(ctx, subprocess.ExtConfig{Name: "model-registry-refresh", Source: fixture, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	command := ext.Commands["model_registry_refresh"]
	if err := command.Handler(ctx, modelRegistryCheck(t, "openai", want[0], nil)); err != nil {
		t.Fatal(err)
	}
	if err := services.Registry().RegisterExtensionProvider("openai", replacement); err != nil {
		t.Error(err)
	}
	if err := command.Handler(ctx, modelRegistryCheck(t, "openai", want[1], map[string]any{})); err != nil {
		t.Fatal(err)
	}
	services.Registry().UnregisterProvider("openai")
	if err := command.Handler(ctx, modelRegistryCheck(t, "openai", want[2], nil)); err != nil {
		t.Fatal(err)
	}
}

func TestSubprocessModelRegistryPublishesProviderRegistrationAndRemoval(t *testing.T) {
	registration := extension.ProviderConfig{
		BaseURL:    "https://models.invalid/v1",
		API:        "openai-completions",
		AuthHeader: false,
		Models: []extension.ProviderModelConfig{
			{ID: "dynamic", Name: "Dynamic"},
			{ID: "org/model/name", Name: "Slash model"},
		},
	}
	find := map[string]any{"find": []string{"registry-refresh", "dynamic", "org/model/name"}}
	want := piModelRegistryObjects(t, find, map[string]any{"register": []any{"registry-refresh", registration}}, find, map[string]any{"unregister": "registry-refresh"}, find)
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	bridge := subprocess.NewUIBridge(func() {})
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	session := newModelRegistryTestSession(t, services)
	detachModelRegistry := wireSubprocessModelRegistry(bridge, session, services)
	defer detachModelRegistry()

	fixture, err := filepath.Abs(filepath.Join("testdata", "model-registry-refresh.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := testbudget.Context(t)
	ext, err := host.Load(ctx, subprocess.ExtConfig{Name: "model-registry-refresh", Source: fixture, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	command, ok := ext.Commands["model_registry_refresh"]
	if !ok {
		t.Fatal("model_registry_refresh command not registered")
	}
	if err := command.Handler(ctx, modelRegistryCheck(t, "", want[0], nil)); err != nil {
		t.Fatal(err)
	}
	if err := services.Registry().RegisterExtensionProvider("registry-refresh", registration); err != nil {
		t.Error(err)
	}
	if err := command.Handler(ctx, modelRegistryCheck(t, "", want[1], nil)); err != nil {
		t.Fatal(err)
	}
	services.Registry().UnregisterProvider("registry-refresh")
	if err := command.Handler(ctx, modelRegistryCheck(t, "", want[2], nil)); err != nil {
		t.Fatal(err)
	}
}
