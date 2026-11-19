//go:build !pig_strip_node_extensions

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

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
			detach := coding.BindSessionModelOperations(bridge, session, services)
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
	detachModelRegistry := coding.BindSessionModelOperations(bridge, session, services)
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
	detachModelRegistry := coding.BindSessionModelOperations(bridge, session, services)
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
	detachModelRegistry := coding.BindSessionModelOperations(bridge, session, services)
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
	detachModelRegistry := coding.BindSessionModelOperations(bridge, session, services)
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
