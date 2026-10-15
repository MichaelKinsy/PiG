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
	if model.ID != "auto" || model.ProviderMeta.API != APIPiMessages || model.ProviderMeta.ProviderID != "radius" || model.ProviderMeta.BaseURL != "https://radius.example.com/v1" || model.Capabilities.CacheWriteCostPer1M != 0.2 {
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

// fetch(new URL("/v1/config", gateway)) throws the URL constructor's TypeError, "Invalid URL", for a gateway Node cannot parse, and fetch refuses a URL with credentials
// ("Request cannot be constructed from a URL that includes credentials: <href>") before any request; neither error names the gateway any other way.
func TestLoadRadiusGatewayConfigThrowsFetchErrorsForInvalidAndCredentialedGateways(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		_, _ = writer.Write([]byte(`{"baseUrl":"https://radius.example/v1","models":[]}`))
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	for _, test := range []struct{ gateway, want string }{
		{"https://radius.example:99999", "Invalid URL"},
		{"radius.example", "Invalid URL"},
		{"http://user:pw@" + host, "Request cannot be constructed from a URL that includes credentials: http://user:pw@" + host + "/v1/config"},
		{"http://:pw@" + host, "Request cannot be constructed from a URL that includes credentials: http://:pw@" + host + "/v1/config"},
		{"http://us er@" + host, "Request cannot be constructed from a URL that includes credentials: http://us%20er@" + host + "/v1/config"},
	} {
		if _, err := LoadRadiusGatewayConfig(context.Background(), test.gateway, "key"); err == nil || err.Error() != test.want {
			t.Errorf("LoadRadiusGatewayConfig(%q) error = %v, want %q", test.gateway, err, test.want)
		}
	}
	// An empty username and password are no credentials: the href drops the "@" and the request goes out.
	if _, err := LoadRadiusGatewayConfig(context.Background(), "http://:@"+host, "key"); err != nil || requests != 1 {
		t.Fatalf("empty userinfo: err = %v, requests = %d", err, requests)
	}
}

// loadRadiusGatewayConfig reads the failure text with response.text() and the config with response.json() (radius-config.ts:88-93). Both drop two
// leading byte order marks and replace each maximal ill-formed UTF-8 subpart with one U+FFFD, as Node 24's Response does.
func TestLoadRadiusGatewayConfigReadsTheBodyAsResponseText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("authorization") == "" {
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte("\xef\xbb\xbf\xef\xbb\xbfbad \xf0\x9d\x84 end"))
			return
		}
		_, _ = writer.Write([]byte("\xef\xbb\xbf\xef\xbb\xbf" + `{"baseUrl":"https://radius.example/v1","models":[]}`))
	}))
	defer server.Close()

	_, err := LoadRadiusGatewayConfig(context.Background(), server.URL, "")
	if want := "Could not load Radius config from " + server.URL + ": 502: bad \uFFFD end"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	config, err := LoadRadiusGatewayConfig(context.Background(), server.URL, "key")
	if err != nil || config.BaseURL != "https://radius.example/v1" {
		t.Fatalf("config = %+v, %v; want the config behind two byte order marks", config, err)
	}
}
