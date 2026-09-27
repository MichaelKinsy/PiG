package main

import (
	"bytes"
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
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func newModelRegistryTestSession(t *testing.T, services *coding.Services) *coding.Session {
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
			services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
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

func TestSubprocessModelRegistryRefreshesConnectedNode(t *testing.T) {
	agentDir := t.TempDir()
	modelsPath := filepath.Join(agentDir, "models.json")
	writeModels := func(data string) {
		t.Helper()
		if err := os.WriteFile(modelsPath, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeModels(`{"providers":{"registry-refresh":{"baseUrl":"https://models.invalid/v1","api":"openai-completions","authHeader":false,"models":[{"id":"remove-me","name":"Remove me"},{"id":"org/model/name","name":"Slash model"}],"modelOverrides":{"org/model/name":{"name":"First override"},"override-only":{"name":"Must stay absent"}}}}}`)

	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
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
	if err := command.Handler(ctx, `{"present":{"remove-me":"Remove me","org/model/name":"First override"},"absent":["added","override-only"]}`); err != nil {
		t.Fatal(err)
	}

	writeModels(`{"providers":{"registry-refresh":{"baseUrl":"https://models.invalid/v1","api":"openai-completions","authHeader":false,"models":[{"id":"added","name":"Added"},{"id":"org/model/name","name":"Slash model"}],"modelOverrides":{"org/model/name":{"name":"Second override"},"override-only":{"name":"Must stay absent"}}}}}`)
	services.Registry().Refresh()

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
	if err := command.Handler(ctx, `{"present":{"added":"Added","org/model/name":"Second override"},"absent":["remove-me","override-only"]}`); err != nil {
		t.Fatal(err)
	}
}

func TestSubprocessModelRegistryPublishesProviderOnlyOverlayRefresh(t *testing.T) {
	agentDir := t.TempDir()
	modelsPath := filepath.Join(agentDir, "models.json")
	writeModels := func(baseURL string, strict bool, version string) {
		t.Helper()
		config := fmt.Sprintf(`{"providers":{"cloudflare-ai-gateway":{"baseUrl":%q,"headers":{"X-Version":%q},"compat":{"supportsStrictMode":%t}}}}`, baseURL, version, strict)
		if err := os.WriteFile(modelsPath, []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeModels("https://first.invalid/v1", false, "first")
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
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
	if err := command.Handler(ctx, `{"provider":"cloudflare-ai-gateway","present":{"gpt-5.6-luna":"GPT-5.6 Luna"},"absent":[],"fields":{"baseUrl":"https://first.invalid/v1"},"compat":{"supportsStrictMode":false},"auth":{"headers":{"X-Version":"first"}}}`); err != nil {
		t.Fatal(err)
	}
	writeModels("https://second.invalid/v1", true, "second")
	services.Registry().Refresh()
	if err := command.Handler(ctx, `{"provider":"cloudflare-ai-gateway","present":{"gpt-5.6-luna":"GPT-5.6 Luna"},"absent":[],"fields":{"baseUrl":"https://second.invalid/v1"},"compat":{"supportsStrictMode":true},"auth":{"headers":{"X-Version":"second"}}}`); err != nil {
		t.Fatal(err)
	}
}

func TestSubprocessModelRegistryPublishesExtensionReplacementAndRestoration(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"openai":{"modelOverrides":{"extension-only":{"name":"Configured extension","reasoning":false,"cost":{"input":0}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
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
	if err := command.Handler(ctx, `{"provider":"openai","present":{"gpt-5.4":"GPT-5.4"},"absent":["extension-only"]}`); err != nil {
		t.Fatal(err)
	}
	services.Registry().RegisterProvider("openai", extension.ProviderConfig{BaseURL: "https://extension.invalid/v1", API: ai.APIOpenAIResponses, Models: []extension.ProviderModelConfig{{ID: "extension-only", Name: "Extension name", API: ai.APIOpenAIResponses, Reasoning: true, Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100, Cost: extension.ProviderModelCost{Input: 9}}}})
	if err := command.Handler(ctx, `{"provider":"openai","present":{"extension-only":"Configured extension"},"absent":["gpt-5.4"],"fields":{"reasoning":false},"auth":{}}`); err != nil {
		t.Fatal(err)
	}
	services.Registry().UnregisterProvider("openai")
	if err := command.Handler(ctx, `{"provider":"openai","present":{"gpt-5.4":"GPT-5.4"},"absent":["extension-only"]}`); err != nil {
		t.Fatal(err)
	}
}

func TestSubprocessModelRegistryPublishesProviderRegistrationAndRemoval(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
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
	if err := command.Handler(ctx, `{"present":{},"absent":["dynamic","org/model/name"]}`); err != nil {
		t.Fatal(err)
	}
	services.Registry().RegisterProvider("registry-refresh", extension.ProviderConfig{
		BaseURL:    "https://models.invalid/v1",
		API:        "openai-completions",
		AuthHeader: false,
		Models: []extension.ProviderModelConfig{
			{ID: "dynamic", Name: "Dynamic"},
			{ID: "org/model/name", Name: "Slash model"},
		},
	})
	if err := command.Handler(ctx, `{"present":{"dynamic":"Dynamic","org/model/name":"Slash model"},"absent":[]}`); err != nil {
		t.Fatal(err)
	}
	services.Registry().UnregisterProvider("registry-refresh")
	if err := command.Handler(ctx, `{"present":{},"absent":["dynamic","org/model/name"]}`); err != nil {
		t.Fatal(err)
	}
}
