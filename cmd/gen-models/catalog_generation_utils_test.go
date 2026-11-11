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
	"slices"
	"testing"
	"time"
)

const (
	catalogEnvironment    = "PIG_GENMODELS_TEST_CATALOG"
	subprocessTestPattern = "^TestGeneratorSubprocess$"
)

// upstreamMockResponses ports writeUpstreamMock of packages/ai/test/catalog-generation-utils.ts: the given models.dev v2 catalog plus
// the minimal responses of the other sources. Any other URL fails.
func upstreamMockResponses(modelsDev any) map[string]any {
	return map[string]any{
		"https://models.dev/experimental/v2.0/api.json": modelsDev,
		"https://models.dev/models.json?type=decision": map[string]any{
			"typesafe/jev-latest": map[string]any{"id": "typesafe/jev-latest", "name": "Jev", "type": "decision", "limit": map[string]any{"context": 64000}},
		},
		"https://openrouter.ai/api/v1/models": map[string]any{"data": []any{}},
		"https://openrouter.ai/api/v1/models?output_modalities=image": map[string]any{"data": []any{
			map[string]any{
				"id": "test/image", "name": "Test Image",
				"architecture": map[string]any{"input_modalities": []any{"text"}, "output_modalities": []any{"image"}},
				"pricing":      map[string]any{},
			},
		}},
		"https://openrouter.ai/api/v1/models?output_modalities=decisions": map[string]any{"data": []any{}},
		"https://ai-gateway.vercel.sh/v1/models":                          map[string]any{"data": []any{}},
		"https://radius.pi.dev/v1/config": map[string]any{
			"baseUrl": "https://radius.pi.dev",
			"models": []any{map[string]any{
				"id": "test", "name": "Test", "reasoning": false, "input": []any{"text"},
				"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
				"contextWindow": 4096, "maxTokens": 4096,
			}},
		},
	}
}

// mockTransport serves the encoded responses by URL and fails every other request like the upstream fetch mock.
type mockTransport map[string][]byte

func (transport mockTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	data, ok := transport[request.URL.String()]
	if !ok {
		return nil, fmt.Errorf("Unexpected fetch: %s", request.URL)
	}
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
}

// TestGeneratorSubprocess is the body of the re-executed test binary: it replaces only the network, as the upstream `--import`
// preload replaces fetch, then runs the real command main with the arguments after `--`, keeping its exit status, stdout and stderr.
func TestGeneratorSubprocess(t *testing.T) {
	path := os.Getenv(catalogEnvironment)
	if path == "" {
		return
	}
	catalog, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var modelsDev any
	if err := json.Unmarshal(catalog, &modelsDev); err != nil {
		t.Fatal(err)
	}
	transport := mockTransport{}
	for url, response := range upstreamMockResponses(modelsDev) {
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		transport[url] = encoded
	}
	http.DefaultTransport = transport
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
}

// runGeneratorSubprocess runs the generator in cwd over the mocked sources, whose models.dev catalog is the given value, and
// returns its exit status, stdout and stderr.
func runGeneratorSubprocess(t *testing.T, cwd string, modelsDev any, args ...string) (int, string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(modelsDev)
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := t.TempDir() + "/mock-catalog.json"
	writeFile(t, catalogPath, string(encoded))
	// upstream: packages/ai/test/fireworks-model-generation.test.ts:54; generate-models-strict.test.ts:72 (timeout: 10_000)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, append([]string{"-test.run=" + subprocessTestPattern, "--"}, args...)...)
	command.Dir = cwd
	command.Env = append(os.Environ(), catalogEnvironment+"="+catalogPath)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if command.ProcessState == nil || ctx.Err() != nil {
		t.Fatalf("generator process failed: %v\n%s\n%s", err, &stdout, &stderr)
	}
	return command.ProcessState.ExitCode(), stdout.String(), stderr.String()
}

// sourceModel is a models.dev v2 chat model that supports tools and has no reasoning, as sourceModel of opencode-model-generation.test.ts.
func sourceModel(id string, api map[string]any) map[string]any {
	return map[string]any{
		"id": id, "type": "chat", "name": id,
		"modalities":   map[string]any{"input": []any{"text"}, "output": []any{"text"}},
		"capabilities": map[string]any{"tools": map[string]any{"supported": true}, "reasoning": map[string]any{"supported": false}},
		"api":          api,
	}
}

// providerCatalog is a models.dev v2 catalog of one provider, keyed by model id like Object.fromEntries(models.map(...)).
func providerCatalog(provider string, models ...map[string]any) map[string]any {
	byID := map[string]any{}
	for _, model := range models {
		byID[model["id"].(string)] = model
	}
	return map[string]any{provider: map[string]any{"models": byID}}
}

func decodeJSONObject(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return object
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
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		t.Errorf("got %s, want %s", got, want)
	}
}
