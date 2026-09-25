package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeRadiusGatewayURL(t *testing.T) {
	for input, want := range map[string]string{
		"radius.example":           "https://radius.example",
		"https://radius.example//": "https://radius.example",
		"HTTP://localhost:8788/":   "HTTP://localhost:8788",
		DefaultRadiusGateway:       "https://radius.pi.dev",
	} {
		if got := NormalizeRadiusGatewayURL(input); got != want {
			t.Errorf("NormalizeRadiusGatewayURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSanitizeRadiusGatewayConfigKeepsOnlyUpstreamShapedModels(t *testing.T) {
	raw := `{"baseUrl":"https://radius.example/v1","models":[
		{"id":"ok","name":"OK","reasoning":true,"input":["text"],"inputLimits":{"maxRequestBytes":4096,"images":{"resize":{"maxWidth":800}}},"cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":0,"tiers":[{"inputTokensAbove":1000,"input":3,"output":4,"cacheRead":0.2,"cacheWrite":0.3}]},"promptCache":{"short":120},"contextWindow":1000,"maxTokens":100,"samplingParams":{"top_p":0.75},"headers":{"X-Catalog":"catalog"},"compat":{"supportsStrictMode":false}},
		{"id":"no-cost","name":"No cost","reasoning":false,"input":["text"],"cost":null,"contextWindow":1,"maxTokens":1},
		{"id":"array-cost","name":"Array cost","reasoning":false,"input":["text"],"cost":[],"contextWindow":1,"maxTokens":1},
		{"id":"string-window","name":"Bad","reasoning":false,"input":["text"],"cost":{},"contextWindow":"1","maxTokens":1},
		"not-an-object"
	]}`
	config, ok := sanitizeRadiusGatewayConfig(json.RawMessage(raw))
	if !ok || config.BaseURL != "https://radius.example/v1" || len(config.Models) != 1 || config.Models[0].ID != "ok" {
		t.Fatalf("sanitize = %+v, %t", config, ok)
	}
	encoded, err := json.Marshal(config.Models[0])
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		t.Fatal(err)
	}
	cost, _ := metadata["cost"].(map[string]any)
	inputLimits, _ := metadata["inputLimits"].(map[string]any)
	promptCache, _ := metadata["promptCache"].(map[string]any)
	samplingParams, _ := metadata["samplingParams"].(map[string]any)
	headers, _ := metadata["headers"].(map[string]any)
	compat, _ := metadata["compat"].(map[string]any)
	if tiers, _ := cost["tiers"].([]any); len(tiers) != 1 || inputLimits["maxRequestBytes"] != float64(4096) || promptCache["short"] != float64(120) || samplingParams["top_p"] != 0.75 || headers["X-Catalog"] != "catalog" || compat["supportsStrictMode"] != false {
		t.Fatalf("sanitized model metadata = %s", encoded)
	}
	for _, invalid := range []string{`{"models":[]}`, `{"baseUrl":1,"models":[]}`, `{"baseUrl":"x","models":{}}`, `[]`, `null`} {
		if _, ok := sanitizeRadiusGatewayConfig(json.RawMessage(invalid)); ok {
			t.Errorf("sanitize(%s) accepted an invalid config", invalid)
		}
	}
}

func TestGetRadiusModelsBindsLegacyCredentialCatalog(t *testing.T) {
	credential := &Credential{Type: CredentialOAuth, GatewayConfig: json.RawMessage(`{"baseUrl":"https://radius.example.com/v1","models":[{"id":"auto","name":"Radius Auto","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":0.2},"contextWindow":128000,"maxTokens":16384}]}`)}
	models := GetRadiusModels("radius", credential)
	if len(models) != 1 {
		t.Fatalf("models = %+v", models)
	}
	model := models[0]
	if model.ID != "auto" || model.API != APIPiMessages || model.Provider != "radius" || model.BaseURL != "https://radius.example.com/v1" || model.Cost.CacheWrite != 0.2 {
		t.Fatalf("model = %+v", model)
	}
	if got := GetRadiusModels("radius", &Credential{Type: CredentialOAuth}); len(got) != 0 {
		t.Fatalf("credential without gatewayConfig produced %+v", got)
	}
	if got := GetRadiusModels("radius", nil); len(got) != 0 {
		t.Fatalf("nil credential produced %+v", got)
	}
}

func TestLoadRadiusGatewayConfigSendsBearerAndSanitizes(t *testing.T) {
	var authorization, accept, path string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization, accept, path = request.Header.Get("authorization"), request.Header.Get("accept"), request.URL.Path
		_, _ = writer.Write([]byte(`{"baseUrl":"https://radius.example/v1","models":[{"id":"balanced","name":"Fresh","reasoning":true,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":424242,"maxTokens":32000}]}`))
	}))
	defer server.Close()

	config, err := LoadRadiusGatewayConfig(context.Background(), server.URL, "radius-key")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/config" || authorization != "Bearer radius-key" || accept != "application/json" {
		t.Fatalf("request path=%q authorization=%q accept=%q", path, authorization, accept)
	}
	if len(config.Models) != 1 || config.Models[0].ContextWindow != 424242 {
		t.Fatalf("config = %+v", config)
	}

	if _, err := LoadRadiusGatewayConfig(context.Background(), server.URL, ""); err != nil || authorization != "" {
		t.Fatalf("anonymous load err=%v authorization=%q", err, authorization)
	}
}

func TestLoadRadiusGatewayConfigReportsFailures(t *testing.T) {
	body := strings.Repeat("x", 600)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("shape") == "" && request.Header.Get("authorization") == "" {
			http.Error(writer, body, http.StatusUnauthorized)
			return
		}
		_, _ = writer.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	_, err := LoadRadiusGatewayConfig(context.Background(), server.URL, "")
	want := "Could not load Radius config from " + server.URL + ": 401: " + strings.Repeat("x", 512) + "…"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	_, err = LoadRadiusGatewayConfig(context.Background(), server.URL, "key")
	if err == nil || err.Error() != "Invalid Radius config from "+server.URL {
		t.Fatalf("invalid config error = %v", err)
	}
}
