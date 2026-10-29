package modelgen

// pi: packages/ai/catalog/sources/index.ts

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type fakeResponse struct {
	status int
	body   string
}

// serveSources answers the catalog sources of http.DefaultClient from the table and fails every other request like the
// generator's fetch mock (packages/ai/test/catalog-generation-utils.ts).
func serveSources(t *testing.T, responses map[string]fakeResponse) {
	t.Helper()
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		response, ok := responses[request.URL.String()]
		if !ok {
			return nil, &unexpectedFetch{url: request.URL.String()}
		}
		return &http.Response{StatusCode: response.status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte(response.body))), Request: request}, nil
	})
}

type unexpectedFetch struct{ url string }

func (e *unexpectedFetch) Error() string { return "Unexpected fetch: " + e.url }

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = previous }()
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(reader)
		done <- string(data)
	}()
	fn()
	_ = writer.Close()
	return <-done
}

func healthySources() map[string]fakeResponse {
	return map[string]fakeResponse{
		ModelsDevURL:                          {200, `{"openai":{"models":{}}}`},
		ModelsDevDecisionsURL:                 {200, `{"typesafe/jev-latest":{"id":"typesafe/jev-latest","name":"Jev","type":"decision","limit":{"context":64000}}}`},
		"https://openrouter.ai/api/v1/models": {200, `{"data":[]}`},
		"https://openrouter.ai/api/v1/models?output_modalities=image":     {200, `{"data":[{"id":"test/image","name":"Test Image","architecture":{"input_modalities":["text"],"output_modalities":["image"]},"pricing":{}}]}`},
		"https://openrouter.ai/api/v1/models?output_modalities=decisions": {200, `{"data":[]}`},
		"https://ai-gateway.vercel.sh/v1/models":                          {200, `{"data":[]}`},
		"https://radius.pi.dev/v1/config":                                 {200, `{"baseUrl":"https://radius.pi.dev","models":[{"id":"test","name":"Test","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4096,"maxTokens":4096,"extra":1},{"id":"bad"}]}`},
	}
}

func TestLoadCatalogSourcesReadsEverySource(t *testing.T) {
	serveSources(t, healthySources())
	var sources *CatalogSources
	stderr := captureStderr(t, func() {
		var err error
		if sources, err = LoadCatalogSources(true); err != nil {
			t.Fatal(err)
		}
	})
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if got := sources.ModelsDevDecisions["typesafe/jev-latest"]; got.Name != "Jev" || got.Type != "decision" || got.ContextLimit != 64000 {
		t.Errorf("decision metadata = %+v", got)
	}
	if len(sources.OpenRouter.Images) != 1 || len(sources.NvidiaModelIDs) != 0 {
		t.Errorf("openrouter images %d, nvidia ids %d; NVIDIA is only fetched when models.dev lists nvidia models", len(sources.OpenRouter.Images), len(sources.NvidiaModelIDs))
	}
	// Radius keeps every member of a valid model, in order, then api, provider and baseUrl; invalid models are dropped.
	if len(sources.Radius) != 1 {
		t.Fatalf("radius models = %d, want 1", len(sources.Radius))
	}
	if got, want := string(Stringify(sources.Radius[0], "")), `{"id":"test","name":"Test","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4096,"maxTokens":4096,"extra":1,"api":"pi-messages","provider":"radius","baseUrl":"https://radius.pi.dev"}`; got != want {
		t.Errorf("radius model = %s\nwant %s", got, want)
	}
}

func TestLoadCatalogSourcesFetchesNvidiaWhenModelsDevListsIt(t *testing.T) {
	responses := healthySources()
	responses[ModelsDevURL] = fakeResponse{200, `{"nvidia":{"models":{}}}`}
	responses[NvidiaBaseURL+"/models"] = fakeResponse{200, `{"data":[{"id":"Meta/Llama_3"}]}`}
	serveSources(t, responses)
	sources, err := LoadCatalogSources(true)
	if err != nil {
		t.Fatal(err)
	}
	if sources.NvidiaModelIDs["Meta/Llama_3"] != "Meta/Llama_3" || sources.NvidiaModelIDs["meta/llama.3"] != "Meta/Llama_3" || len(sources.NvidiaModelIDs) != 2 {
		t.Errorf("nvidia ids = %v, want the raw and the normalized id of every listed model", sources.NvidiaModelIDs)
	}
}

// A failed source contributes nothing unless generation is strict; strict generation stops at the first failure.
func TestLoadCatalogSourcesFailures(t *testing.T) {
	cases := []struct {
		name       string
		fail       map[string]fakeResponse
		label      string
		wantErr    string
		wantStderr string
	}{
		{"models.dev", map[string]fakeResponse{ModelsDevURL: {500, ``}}, "models.dev catalog", "models.dev API returned 500", "Failed to fetch models.dev catalog: Error: models.dev API returned 500\n"},
		{"decisions", map[string]fakeResponse{ModelsDevDecisionsURL: {404, ``}}, "models.dev decision models", "models.dev decision API returned 404", "Failed to fetch models.dev decision models: Error: models.dev decision API returned 404\n"},
		{"openrouter list", map[string]fakeResponse{"https://openrouter.ai/api/v1/models?output_modalities=decisions": {502, ``}}, "OpenRouter models", "OpenRouter API returned 502", "Failed to fetch OpenRouter models: Error: OpenRouter API returned 502\n"},
		{"vercel", map[string]fakeResponse{"https://ai-gateway.vercel.sh/v1/models": {503, ``}}, "Vercel AI Gateway models", "Vercel AI Gateway API returned 503", "Failed to fetch Vercel AI Gateway models: Error: Vercel AI Gateway API returned 503\n"},
		{"radius status", map[string]fakeResponse{"https://radius.pi.dev/v1/config": {503, "  down for maintenance \n"}}, "Radius models", "Could not load Radius config from https://radius.pi.dev: 503: down for maintenance", "Failed to fetch Radius models: Error: Could not load Radius config from https://radius.pi.dev: 503: down for maintenance\n"},
		{"radius invalid", map[string]fakeResponse{"https://radius.pi.dev/v1/config": {200, `{"baseUrl":1}`}}, "Radius models", "Invalid Radius config from https://radius.pi.dev", "Failed to fetch Radius models: Error: Invalid Radius config from https://radius.pi.dev\n"},
		{"radius empty", map[string]fakeResponse{"https://radius.pi.dev/v1/config": {200, `{"baseUrl":"https://radius.pi.dev","models":[]}`}}, "Radius models", "Radius API returned no models", "Failed to fetch Radius models: Error: Radius API returned no models\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responses := healthySources()
			for url, response := range tc.fail {
				responses[url] = response
			}
			serveSources(t, responses)

			stderr := captureStderr(t, func() {
				if _, err := LoadCatalogSources(false); err != nil {
					t.Errorf("non-strict error = %v, want a source that contributes nothing", err)
				}
			})
			if stderr != tc.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, tc.wantStderr)
			}
			captureStderr(t, func() {
				if _, err := LoadCatalogSources(true); err == nil || err.Error() != tc.wantErr {
					t.Errorf("strict error = %v, want %q", err, tc.wantErr)
				}
			})
		})
	}
}

func TestLoadCatalogSourcesStrictNeedsImageModels(t *testing.T) {
	responses := healthySources()
	responses["https://openrouter.ai/api/v1/models?output_modalities=image"] = fakeResponse{200, `{"data":[]}`}
	serveSources(t, responses)
	if _, err := LoadCatalogSources(true); err == nil || !strings.Contains(err.Error(), "OpenRouter API returned no usable image models") {
		t.Errorf("strict error = %v", err)
	}
	if _, err := LoadCatalogSources(false); err != nil {
		t.Errorf("non-strict error = %v", err)
	}
}
