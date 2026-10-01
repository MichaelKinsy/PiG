package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type catalogFixtureTransport struct {
	catalog []byte
	// openRouter answers the OpenRouter listings by query string; a missing query answers an empty list.
	openRouter map[string]string
}

func (transport catalogFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var data []byte
	url := request.URL.String()
	switch {
	case url == "https://models.dev/api.json":
		data = transport.catalog
	case url == "https://models.dev/models.json?type=decision":
		data = []byte(`{"typesafe/jev-latest":{"name":"Jev","type":"decision","limit":{"context":64000,"output":0}}}`)
	case strings.HasPrefix(url, "https://openrouter.ai/api/v1/models"):
		data = []byte(`{"data":[]}`)
		if listing, ok := transport.openRouter[strings.TrimPrefix(url, "https://openrouter.ai/api/v1/models")]; ok {
			data = []byte(listing)
		}
	case url == "https://ai-gateway.vercel.sh/v1/models":
		data = []byte(`{"data":[]}`)
	case url == "https://radius.pi.dev/v1/config":
		data = []byte(`{"baseUrl":"https://radius.pi.dev","models":[{"id":"test","name":"Test","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4096,"maxTokens":4096}]}`)
	default:
		return nil, fmt.Errorf("Unexpected fetch: %s", request.URL)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
}

// TestPortWave12GeneratorSubprocess replaces only fetch, as the upstream --import preload does. The subprocess runs the real command main and preserves its exit status/stdout/stderr.
func TestPortWave12GeneratorSubprocess(t *testing.T) {
	catalog := os.Getenv("PIG_WAVE12_CATALOG")
	if catalog == "" {
		return
	}
	data, err := os.ReadFile(catalog)
	if err != nil {
		t.Fatal(err)
	}
	transport := catalogFixtureTransport{catalog: data}
	if listings := os.Getenv("PIG_WAVE12_OPENROUTER"); listings != "" {
		encoded, err := os.ReadFile(listings)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &transport.openRouter); err != nil {
			t.Fatal(err)
		}
	}
	http.DefaultClient = &http.Client{Transport: transport}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing generator arguments")
	}
	os.Args = append([]string{os.Args[0]}, os.Args[separator+1:]...)
	flag.CommandLine = flag.NewFlagSet("gen-models", flag.ExitOnError)
	main()
	os.Exit(0)
}

func isolateGeneratorEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HOME", "PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(name, t.TempDir())
	}
	for _, name := range []string{"PI_SESSION_FILE", "PI_SESSION_ID", "PIG_SDK_GO_ROOT"} {
		t.Setenv(name, "")
	}
}

func runGeneratorSubprocess(t *testing.T, cwd string, catalog any, args ...string) (int, string, string) {
	t.Helper()
	return runGeneratorSubprocessWithOpenRouter(t, cwd, catalog, nil, args...)
}

// runGeneratorSubprocessWithOpenRouter also answers the OpenRouter listings: openRouter maps a query string ("", "?output_modalities=image", ...) to a response body.
func runGeneratorSubprocessWithOpenRouter(t *testing.T, cwd string, catalog any, openRouter map[string]string, args ...string) (int, string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	preload := filepath.Join(t.TempDir(), "models-dev.json")
	writeFile(t, preload, string(encoded))
	// upstream: packages/ai/test/fireworks-model-generation.test.ts:55; generate-models-strict.test.ts:73
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, append([]string{"-test.run=^TestPortWave12GeneratorSubprocess$", "--"}, args...)...)
	command.Dir = cwd
	command.Env = append(os.Environ(), "PIG_WAVE12_CATALOG="+preload)
	if openRouter != nil {
		encoded, err := json.Marshal(openRouter)
		if err != nil {
			t.Fatal(err)
		}
		listings := filepath.Join(t.TempDir(), "openrouter.json")
		writeFile(t, listings, string(encoded))
		command.Env = append(command.Env, "PIG_WAVE12_OPENROUTER="+listings)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if command.ProcessState == nil || ctx.Err() != nil {
		t.Fatalf("generator process failed: %v\n%s\n%s", err, &stdout, &stderr)
	}
	return command.ProcessState.ExitCode(), stdout.String(), stderr.String()
}

// upstream: packages/ai/test/fireworks-model-generation.test.ts:21-62
func generateFireworksFixture(t *testing.T, options map[string][]modelsDevReasoningOption) map[string]json.RawMessage {
	t.Helper()
	root := t.TempDir()
	sources := make(map[string]any)
	for id, reasoning := range options {
		model := map[string]any{"id": id, "tool_call": true, "reasoning": true}
		if reasoning != nil {
			model["reasoning_options"] = reasoning
		}
		sources["accounts/fireworks/models/"+id] = model
	}
	catalog := map[string]any{"fireworks-ai": map[string]any{"models": sources}}
	output := filepath.Join(root, "catalog")
	status, stdout, stderr := runGeneratorSubprocess(t, root, catalog, "--json-only", "--json-output", output)
	if status != 0 || stderr != "" {
		t.Fatalf("generator status=%d, want 0; stderr=%q, want empty\nstdout=%s", status, stderr, stdout)
	}
	data, err := os.ReadFile(filepath.Join(output, "providers", "fireworks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var models map[string]json.RawMessage
	if err := json.Unmarshal(data, &models); err != nil {
		t.Fatal(err)
	}
	return models
}

func generatedModelField(t *testing.T, raw json.RawMessage, name string) json.RawMessage {
	t.Helper()
	var model map[string]json.RawMessage
	if err := json.Unmarshal(raw, &model); err != nil {
		t.Fatal(err)
	}
	return model[name]
}

func assertGeneratedJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatalf("decode actual %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	a, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("got %s, want %s", got, want)
	}
}

func requireMessagesModel(t *testing.T, raw json.RawMessage) {
	t.Helper()
	assertGeneratedJSON(t, generatedModelField(t, raw, "api"), `"anthropic-messages"`)
}

func effortOptions(values ...string) modelsDevReasoningOption {
	option := modelsDevReasoningOption{Type: "effort"}
	for _, value := range values {
		option.Values = append(option.Values, new(value))
	}
	return option
}

func upstreamAIPackage(t *testing.T) string {
	t.Helper()
	source := filepath.Join("../..", ".upstream", "v"+pigversion.UpstreamVersion, "packages", "ai")
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	// The release source omits generated provider data. The mirror hydrates visible JSON shards, but the strict test also snapshots the published hidden manifest.
	// upstream: packages/ai/test/generate-models-strict.test.ts:59-66.
	published := filepath.Join("../..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "@earendil-works", "pi-ai")
	data, err := os.ReadFile(filepath.Join(published, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct{ Version string }
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != pigversion.UpstreamVersion {
		t.Fatalf("published pi-ai version = %q, want %q", manifest.Version, pigversion.UpstreamVersion)
	}
	data, err = os.ReadFile(filepath.Join(published, "dist", "providers", "data", ".manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "src", "providers", "data", ".manifest.json"), string(data))
	return root
}
