package coding

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestBuildModelUsesGeneratedProviderAPI(t *testing.T) {
	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		spec       string
		providerTy string
	}{
		{"openai/gpt-4", "*ai.openAIResponsesProvider"},
		{"openai-codex/gpt-5.3-codex-spark", "*ai.openAIResponsesProvider"},
		{"anthropic/claude-haiku-4-5", "*ai.anthropicProvider"},
		{"google/gemini-2.5-flash", "*ai.googleProvider"},
		{"google-vertex/gemini-2.5-flash", "*ai.googleProvider"},
		{"mistral/codestral-latest", "*ai.mistralProvider"},
		{"amazon-bedrock/amazon.nova-2-lite-v1:0", "*ai.BedrockProvider"},
		{"xai/grok-4.3", "*ai.openAIResponsesProvider"},
	}

	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			model, err := BuildModel(tc.spec, svcs)
			if err != nil {
				t.Fatalf("BuildModel: %v", err)
			}
			attributed, ok := model.Provider.(*providerAttributionProvider)
			if !ok {
				t.Fatalf("provider type = %T, want *coding.providerAttributionProvider", model.Provider)
			}
			if got := fmt.Sprintf("%T", attributed.Provider); got != tc.providerTy {
				t.Fatalf("underlying provider type = %s, want %s", got, tc.providerTy)
			}
			providerID, _, _ := strings.Cut(tc.spec, "/")
			if got := model.Provider.ID(); got != providerID {
				t.Fatalf("provider ID = %q, want %q", got, providerID)
			}
			if model.ProviderMeta.BaseURL == "" && providerID != "azure-openai-responses" {
				t.Fatalf("ProviderMeta.BaseURL empty for generated model %s", tc.spec)
			}
		})
	}
}

// A models.json provider with "api": "pi-messages" streams through the
// Services-owned ModelRuntime to <baseUrl>/messages (pi-messages.ts header
// comment: any backend implementing the protocol can be used this way).
func TestPiMessagesModelsJSONProviderStreamsThroughModelRuntime(t *testing.T) {
	var path, authorization string
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path, authorization = request.URL.Path, request.Header.Get("authorization")
		writer.Header().Set("content-type", "text/event-stream")
		for _, event := range []string{
			`{"type":"start"}`,
			`{"type":"text_start","contentIndex":0}`,
			`{"type":"text_end","contentIndex":0,"content":"hi"}`,
			`{"type":"done","reason":"stop","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`,
		} {
			_, _ = io.WriteString(writer, "data: "+event+"\n\n")
		}
	}))
	defer gateway.Close()
	agentDir := t.TempDir()
	config := `{"providers":{"gateway":{"baseUrl":"` + gateway.URL + `/v1","api":"pi-messages","apiKey":"gw-key","models":[{"id":"auto","name":"Gateway Auto"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model := svcs.ModelRuntime().GetModel("gateway", "auto")
	if model == nil || model.ProviderMeta.API != ai.APIPiMessages {
		t.Fatalf("model = %#v", model)
	}

	message := svcs.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}}}, ai.StreamOptions{})

	if message.StopReason != ai.StopReasonStop || len(message.Content) != 1 || message.Content[0] != (ai.TextContent{Text: "hi"}) {
		t.Fatalf("message = %+v", message)
	}
	if path != "/v1/messages" || authorization != "Bearer gw-key" {
		t.Fatalf("request path=%q authorization=%q", path, authorization)
	}
}

func TestPublishedRadiusMetadataReachesModelRuntime(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "fixture-key")
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("radius", "gpt-5.6-luna")
	if model == nil {
		t.Fatal("published Radius model missing")
	}
	if len(model.Capabilities.CostTiers) != 1 || model.Capabilities.CostTiers[0].InputTokensAbove != 272000 {
		t.Fatalf("published Radius cost tiers = %#v", model.Capabilities.CostTiers)
	}
	if model.InputLimits == nil || model.InputLimits.Images == nil || model.InputLimits.Images.Resize == nil || model.InputLimits.Images.Resize.MaxWidth != 2000 {
		t.Fatalf("published Radius input limits = %#v", model.InputLimits)
	}
}

func TestBuildModelDoesNotAliasGeneratedFallbackMetadata(t *testing.T) {
	generated, ok := ai.LookupModelExact("anthropic/claude-fable-5")
	if !ok || generated.Compat == nil || len(generated.Compat.AllowedFallbackModels) == 0 {
		t.Fatal("anthropic/claude-fable-5 fallback metadata missing")
	}
	originalTiers := generated.Compat.AllowedFallbackModels[0].Cost.Tiers
	generated.Compat.AllowedFallbackModels[0].Cost.Tiers = []ai.CostTier{{InputTokensAbove: 1, InputCostPer1M: 2}}
	defer func() { generated.Compat.AllowedFallbackModels[0].Cost.Tiers = originalTiers }()

	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("anthropic/claude-fable-5", svcs)
	if err != nil {
		t.Fatal(err)
	}
	model.ProviderMeta.Compat.AllowedFallbackModels[0].Model = "mutated"
	model.ProviderMeta.Compat.AllowedFallbackModels[0].Cost.Tiers[0].InputCostPer1M = 99
	if generated.Compat.AllowedFallbackModels[0].Model != "claude-opus-4-8" {
		t.Fatal("BuildModel aliased the generated fallback-model slice")
	}
	if generated.Compat.AllowedFallbackModels[0].Cost.Tiers[0].InputCostPer1M != 2 {
		t.Fatal("BuildModel aliased the generated fallback cost tiers")
	}
}

// TestBuildModelGatesAttributionHeadersOnInstallTelemetrySetting drives the
// production path (BuildModel -> Services.ModelRuntime().Complete) rather than
// calling mergeProviderAttributionHeaders directly, proving
// SettingsManager.SetEnableInstallTelemetry actually reaches the wire request.
// Mirrors upstream getDefaultAttributionHeaders' telemetry gate
// (provider-attribution.ts:40); pig divergence (D26) covers the branding.
func TestBuildModelGatesAttributionHeadersOnInstallTelemetrySetting(t *testing.T) {
	var gotHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		gotHeader = request.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	agentDir := t.TempDir()
	models := fmt.Sprintf(`{"providers":{"openrouter":{"baseUrl":%q,"api":"openai-completions","authHeader":false,"models":[{"id":"model","name":"Model"}]}}}`, server.URL+"/v1")
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}

	if err := services.SettingsManager().SetEnableInstallTelemetry(false); err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("openrouter/model", services)
	if err != nil {
		t.Fatal(err)
	}
	result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}, ai.StreamOptions{})
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	if gotHeader.Get("HTTP-Referer") != "" || gotHeader.Get("X-OpenRouter-Title") != "" || gotHeader.Get("X-OpenRouter-Categories") != "" {
		t.Fatalf("expected no attribution headers with install telemetry disabled, got %#v", gotHeader)
	}

	if err := services.SettingsManager().SetEnableInstallTelemetry(true); err != nil {
		t.Fatal(err)
	}
	model, err = BuildModel("openrouter/model", services)
	if err != nil {
		t.Fatal(err)
	}
	result = services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}, ai.StreamOptions{})
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	if got := gotHeader.Get("HTTP-Referer"); got != "https://github.com/MichaelKinsy/PiG" {
		t.Fatalf("HTTP-Referer = %q, want pig-branded referer", got)
	}
	if got := gotHeader.Get("X-OpenRouter-Title"); got != "PiG" {
		t.Fatalf("X-OpenRouter-Title = %q, want PiG", got)
	}
	if got := gotHeader.Get("X-OpenRouter-Categories"); got != "cli-agent" {
		t.Fatalf("X-OpenRouter-Categories = %q, want cli-agent", got)
	}
}

// A models.json Radius gateway refreshes its catalog with the stored OAuth
// credential, then streams through the ModelRuntime with a token refreshed
// at request time (upstream getAuth refreshes an expired credential).
func TestRadiusGatewayCatalogAndStreamThroughModelRuntime(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	var messageCatalogHeader string
	var gatewayURL string
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		seen = append(seen, request.URL.Path+" "+request.Header.Get("authorization"))
		mu.Unlock()
		switch request.URL.Path {
		case "/v1/oauth/token":
			writer.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(writer, `{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":3600}`)
		case "/v1/config":
			writer.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(writer, `{"baseUrl":"`+gatewayURL+`/v1","models":[{"id":"auto","name":"Radius Auto","reasoning":false,"input":["text","image"],"inputLimits":{"maxRequestBytes":4096,"images":{"resize":{"maxWidth":800}}},"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":1000,"input":3,"output":4,"cacheRead":0.2,"cacheWrite":0.3}]},"promptCache":{"short":120},"contextWindow":128000,"maxTokens":16384,"samplingParams":{"top_p":0.75},"headers":{"X-Catalog":"catalog"},"compat":{"supportsStrictMode":false}}]}`)
		case "/v1/messages":
			messageCatalogHeader = request.Header.Get("X-Catalog")
			writer.Header().Set("content-type", "text/event-stream")
			_, _ = io.WriteString(writer, "data: {\"type\":\"start\"}\n\ndata: {\"type\":\"done\",\"reason\":\"stop\",\"usage\":{\"input\":1,\"output\":1,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":2,\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"total\":0}}}\n\n")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer gateway.Close()
	gatewayURL = gateway.URL
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"radius-dev":{"name":"Radius (dev)","baseUrl":"`+gateway.URL+`/v1","oauth":"radius"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("radius-dev", ai.Credential{Type: ai.CredentialOAuth, Access: "stale", Refresh: "old-refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if model := svcs.ModelRuntime().GetModel("radius-dev", "auto"); model != nil {
		t.Fatalf("catalog model present before any network refresh: %+v", model)
	}

	result := svcs.Registry().RefreshCatalogs(context.Background(), icodingagent.CatalogRefreshOptions{AllowNetwork: true})
	if len(result.Errors) != 0 {
		t.Fatalf("refresh errors = %v", result.Errors)
	}
	model := svcs.ModelRuntime().GetModel("radius-dev", "auto")
	if model == nil || model.ProviderMeta.API != ai.APIPiMessages || model.ProviderMeta.BaseURL != gateway.URL+"/v1" {
		t.Fatalf("model = %+v", model)
	}
	if len(model.Capabilities.CostTiers) != 1 || model.Capabilities.CostTiers[0].InputTokensAbove != 1000 || model.InputLimits == nil || model.InputLimits.MaxRequestBytes != 4096 || model.InputLimits.Images == nil || model.InputLimits.Images.Resize == nil || model.InputLimits.Images.Resize.MaxWidth != 800 {
		t.Fatalf("catalog cost/input metadata = capabilities %+v inputLimits %+v", model.Capabilities, model.InputLimits)
	}
	if model.PromptCache["short"] != 120 || model.SamplingParams["top_p"] != float64(0.75) || model.ProviderMeta.Headers["X-Catalog"] != "catalog" || model.ProviderMeta.Compat == nil || model.ProviderMeta.Compat.SupportsStrictMode == nil || *model.ProviderMeta.Compat.SupportsStrictMode {
		t.Fatalf("catalog request metadata = promptCache %v sampling %v provider %+v", model.PromptCache, model.SamplingParams, model.ProviderMeta)
	}
	message := svcs.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}}, ai.StreamOptions{})
	if message.StopReason != ai.StopReasonStop {
		t.Fatalf("message = %+v", message)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/v1/oauth/token ", "/v1/config Bearer fresh-access", "/v1/messages Bearer fresh-access"}
	if !slices.Equal(seen, want) {
		t.Fatalf("gateway requests = %q, want %q", seen, want)
	}
	if messageCatalogHeader != "catalog" {
		t.Fatalf("catalog request header = %q", messageCatalogHeader)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "models-store.json")); err != nil {
		t.Fatalf("models-store.json was not written: %v", err)
	}
	restoredServices, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	restored := restoredServices.ModelRuntime().GetModel("radius-dev", "auto")
	if restored == nil || len(restored.Capabilities.CostTiers) != 1 || restored.InputLimits == nil || restored.PromptCache["short"] != 120 || restored.ProviderMeta.Headers["X-Catalog"] != "catalog" {
		t.Fatalf("restored catalog metadata = %+v", restored)
	}
}
