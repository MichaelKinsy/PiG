package ai

// Ports packages/ai/test/identity.test.ts.

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

var acmeAttribution = ClientAttribution{Name: "acme", URL: "https://acme.example"}

func identityTarget(provider, baseURL string) ClientIdentityTarget {
	return ClientIdentityTarget{Provider: provider, BaseURL: baseURL}
}

func TestClientIdentityVersionDefaultsToTheProductVersion(t *testing.T) {
	if got := GetClientVersion(nil); got != ProductVersion {
		t.Fatalf("GetClientVersion(nil) = %q, want %q", got, ProductVersion)
	}
	if got := GetClientVersion(&ClientIdentity{Version: new("9.9.9")}); got != "9.9.9" {
		t.Fatalf("GetClientVersion = %q, want 9.9.9", got)
	}
}

// pig divergence (D65): the detailed form is pig/<version> (<platform> <release>; <arch>) where Pi sends pi/<version> (<platform>; <runtime>; <arch>); short is the product name.
func TestFormatUserAgentForms(t *testing.T) {
	detailed := FormatUserAgent(nil)
	if want := "pig/" + ProductVersion + " ("; !strings.HasPrefix(detailed, want) {
		t.Fatalf("FormatUserAgent(nil) = %q, want prefix %q", detailed, want)
	}
	if got, want := FormatUserAgent(&ClientIdentity{Version: new("9.9.9")}), strings.Replace(detailed, ProductVersion, "9.9.9", 1); got != want {
		t.Fatalf("version override = %q, want %q", got, want)
	}
	if got := FormatUserAgent(&ClientIdentity{UserAgent: new(UserAgentShort), Version: new("9.9.9")}); got != pigidentity.UserAgentProduct {
		t.Fatalf("short = %q, want %q", got, pigidentity.UserAgentProduct)
	}
	if got := FormatUserAgent(&ClientIdentity{UserAgent: new(UserAgentCustom("acme/1.0"))}); got != "acme/1.0" {
		t.Fatalf("custom = %q, want acme/1.0", got)
	}
	if got := FormatUserAgent(&ClientIdentity{UserAgent: new(UserAgentDetailed)}); got != detailed {
		t.Fatalf("explicit detailed = %q, want %q", got, detailed)
	}
}

func TestMergeClientIdentityFieldByField(t *testing.T) {
	if got := MergeClientIdentity(nil, nil); got != nil {
		t.Fatalf("merge of nothing = %#v, want nil", got)
	}
	got := MergeClientIdentity(
		&ClientIdentity{Attribution: &PiAttribution, UserAgent: new(UserAgentShort)},
		&ClientIdentity{Version: new("1.0.0")},
	)
	if want := (&ClientIdentity{Attribution: &PiAttribution, UserAgent: new(UserAgentShort), Version: new("1.0.0")}); !reflect.DeepEqual(got, want) {
		t.Fatalf("merge = %#v, want %#v", got, want)
	}
	// null attribution turns off the attribution of an earlier identity.
	got = MergeClientIdentity(&ClientIdentity{Attribution: &PiAttribution}, &ClientIdentity{AttributionNull: true})
	if want := (&ClientIdentity{AttributionNull: true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("null attribution merge = %#v, want %#v", got, want)
	}
	// A later identity does not change the earlier ones.
	first := &ClientIdentity{UserAgent: new(UserAgentShort)}
	_ = MergeClientIdentity(first, &ClientIdentity{Version: new("2")})
	if first.Version != nil {
		t.Fatal("merge mutated its first source")
	}
}

func TestClientIdentityJSON(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity ClientIdentity
		json     string
	}{
		{"empty", ClientIdentity{}, `{}`},
		{"short", ClientIdentity{UserAgent: new(UserAgentShort)}, `{"userAgent":"short"}`},
		{"detailed", ClientIdentity{UserAgent: new(UserAgentDetailed), Version: new("1.2.3")}, `{"userAgent":"detailed","version":"1.2.3"}`},
		{"custom", ClientIdentity{UserAgent: new(UserAgentCustom("acme/1.0"))}, `{"userAgent":{"custom":"acme/1.0"}}`},
		{"null attribution", ClientIdentity{AttributionNull: true}, `{"attribution":null}`},
		{"attribution", ClientIdentity{Attribution: &ClientAttribution{Name: "acme", URL: "https://acme.example", Categories: []string{"cli-agent"}}}, `{"attribution":{"name":"acme","url":"https://acme.example","categories":["cli-agent"]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.identity)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.json {
				t.Fatalf("encoded = %s, want %s", encoded, tc.json)
			}
			var decoded ClientIdentity
			if err := json.Unmarshal([]byte(tc.json), &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, tc.identity) {
				t.Fatalf("decoded = %#v, want %#v", decoded, tc.identity)
			}
		})
	}
	var bad ClientIdentity
	if err := json.Unmarshal([]byte(`{"userAgent":"loud"}`), &bad); err == nil {
		t.Fatal("an unknown user agent mode decoded")
	}
}

func TestIdentitySendsNoAttributionUnlessEnabled(t *testing.T) {
	openRouter := identityTarget("openrouter", "https://openrouter.ai/api/v1")
	if got := GetAttributionHeaders(openRouter, nil); len(got) != 0 {
		t.Fatalf("no identity = %v, want none", got)
	}
	if got := GetAttributionHeaders(openRouter, &ClientIdentity{AttributionNull: true}); len(got) != 0 {
		t.Fatalf("null attribution = %v, want none", got)
	}
	if got, want := GetIdentityHeaders(openRouter, nil), map[string]string{"User-Agent": FormatUserAgent(nil)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("identity headers = %v, want %v", got, want)
	}
}

// pig divergence (D26): PiAttribution carries PiG's name, URL and category where Pi's PI_ATTRIBUTION carries `pi`, `https://pi.dev` and `cli-agent`.
func TestIdentityAttributesOpenRouterRequests(t *testing.T) {
	openRouter := identityTarget("openrouter", "https://openrouter.ai/api/v1")
	piG := &ClientIdentity{Attribution: &PiAttribution}
	if got, want := GetAttributionHeaders(openRouter, piG), map[string]string{"HTTP-Referer": pigidentity.AttributionURL, "X-OpenRouter-Title": pigidentity.AttributionName, "X-OpenRouter-Categories": pigidentity.AttributionCategory}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PiG attribution = %v, want %v", got, want)
	}
	acme := &ClientAttribution{Name: "acme", URL: "https://acme.example", Categories: []string{"cli-agent", "cloud-agent"}}
	if got, want := GetAttributionHeaders(openRouter, &ClientIdentity{Attribution: acme}), map[string]string{"HTTP-Referer": "https://acme.example", "X-OpenRouter-Title": "acme", "X-OpenRouter-Categories": "cli-agent,cloud-agent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}
	empty := &ClientAttribution{Name: "acme", URL: "https://acme.example", Categories: []string{}}
	if got, want := GetAttributionHeaders(openRouter, &ClientIdentity{Attribution: empty}), map[string]string{"HTTP-Referer": "https://acme.example", "X-OpenRouter-Title": "acme"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty categories = %v, want %v", got, want)
	}
	// Custom providers routed through OpenRouter, including legacy base URLs that are not valid URLs.
	if got := GetAttributionHeaders(identityTarget("custom", "not-a-url-openrouter.ai"), piG); got["X-OpenRouter-Title"] != pigidentity.AttributionName {
		t.Fatalf("legacy base URL = %v", got)
	}
	// OpenRouter keeps Pi's case-sensitive substring rule.
	if got := GetAttributionHeaders(identityTarget("custom", "https://OPENROUTER.AI/v1"), piG); len(got) != 0 {
		t.Fatalf("upper-case OpenRouter host = %v, want none", got)
	}
}

func TestIdentityAttributesNVIDIAWithACapitalizedNameAndOpenCodeWithTheName(t *testing.T) {
	for _, tc := range []struct {
		target      ClientIdentityTarget
		attribution ClientAttribution
		want        map[string]string
	}{
		{identityTarget("nvidia", "https://example.test/v1"), PiAttribution, map[string]string{"X-BILLING-INVOKE-ORIGIN": pigidentity.AttributionName}},
		{identityTarget("custom-nim", "https://integrate.api.nvidia.com/v1"), acmeAttribution, map[string]string{"X-BILLING-INVOKE-ORIGIN": "Acme"}},
		{identityTarget("opencode", "https://opencode.ai/zen/v1"), PiAttribution, map[string]string{"x-opencode-client": pigidentity.AttributionName}},
		{identityTarget("custom", "https://opencode.ai/zen/v1"), acmeAttribution, map[string]string{"x-opencode-client": "acme"}},
		// The host match is WHATWG hostname equality: case-insensitive for DNS hosts, never a substring, userinfo or path match.
		{identityTarget("custom", "https://INTEGRATE.API.NVIDIA.COM:443/v1"), acmeAttribution, map[string]string{"X-BILLING-INVOKE-ORIGIN": "Acme"}},
		{identityTarget("custom", "https://OPENCODE.AI/v1"), acmeAttribution, map[string]string{"x-opencode-client": "acme"}},
		{identityTarget("custom", "https://INTEGRATE.API.NVIDIA.COM.example.test/v1"), acmeAttribution, map[string]string{}},
		{identityTarget("custom", "https://INTEGRATE.API.NVIDIA.COM@other.test/v1"), acmeAttribution, map[string]string{}},
		{identityTarget("custom", "https://other.test/opencode.ai"), acmeAttribution, map[string]string{}},
		{identityTarget("custom", "https://opencode.ai./v1"), acmeAttribution, map[string]string{}},
		{identityTarget("custom", ""), acmeAttribution, map[string]string{}},
		{identityTarget("custom", "://opencode.ai"), acmeAttribution, map[string]string{}},
		// A non-special scheme keeps its opaque host as written.
		{identityTarget("custom", "custom://OPENCODE.AI/x"), acmeAttribution, map[string]string{}},
		{identityTarget("custom", "custom://opencode.ai/x"), acmeAttribution, map[string]string{"x-opencode-client": "acme"}},
	} {
		attribution := tc.attribution
		if got := GetAttributionHeaders(tc.target, &ClientIdentity{Attribution: &attribution}); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: headers = %v, want %v", tc.target, got, tc.want)
		}
	}
}

func TestIdentityDoesNotAttributeNVIDIAModelsRoutedThroughGateways(t *testing.T) {
	identity := &ClientIdentity{Attribution: &PiAttribution}
	if got := GetAttributionHeaders(identityTarget("openrouter", "https://openrouter.ai/api/v1"), identity); got["X-BILLING-INVOKE-ORIGIN"] != "" {
		t.Fatalf("OpenRouter headers = %v, want no NVIDIA origin", got)
	}
	if got := GetAttributionHeaders(identityTarget("vercel-ai-gateway", "https://ai-gateway.vercel.sh/v1"), identity); len(got) != 0 {
		t.Fatalf("Vercel AI Gateway headers = %v, want none", got)
	}
	// Cloudflare no longer has a User-Agent attribution (Pi removed provider-attribution.ts's Cloudflare case).
	for _, target := range []ClientIdentityTarget{identityTarget("cloudflare-workers-ai", "https://api.cloudflare.com/client/v4"), identityTarget("cloudflare-ai-gateway", "https://gateway.ai.cloudflare.com/v1")} {
		if got := GetAttributionHeaders(target, identity); len(got) != 0 {
			t.Fatalf("%s headers = %v, want none", target.Provider, got)
		}
	}
}

func TestGetOpenCodeSessionHeaders(t *testing.T) {
	if got := GetOpenCodeSessionHeaders(identityTarget("custom", "https://opencode.ai/zen/v1"), "session-1"); got == nil || got["x-opencode-session"] == nil || *got["x-opencode-session"] != "session-1" || len(got) != 1 {
		t.Fatalf("OpenCode target = %v", got)
	}
	if got := GetOpenCodeSessionHeaders(identityTarget("opencode-go", ""), "session-1"); got == nil {
		t.Fatal("opencode-go provider has no session header")
	}
	if got := GetOpenCodeSessionHeaders(identityTarget("opencode", "https://opencode.ai"), ""); got != nil {
		t.Fatalf("no session = %v, want nil", got)
	}
	if got := GetOpenCodeSessionHeaders(identityTarget("openrouter", "https://openrouter.ai"), "session-1"); got != nil {
		t.Fatalf("other target = %v, want nil", got)
	}
}

// identityRequestHeaders streams one openai-completions request through a provider whose transport records the headers.
func identityRequestHeaders(t *testing.T, config OpenAIConfig, options StreamOptions) http.Header {
	t.Helper()
	provider, ok := NewOpenAIProvider(config).(*openAIProvider)
	if !ok {
		t.Fatal("NewOpenAIProvider did not return *openAIProvider")
	}
	var header http.Header
	provider.client = sseClient(&header, userAgentOpenAICompletionsSSE)
	drainProviderStream(t, provider, options)
	return header
}

func TestIdentityAppliesBelowModelAndRequestHeaders(t *testing.T) {
	header := identityRequestHeaders(t, OpenAIConfig{ProviderID: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Model: "m", ExtraHeaders: map[string]string{"X-OpenRouter-Title": "model-title"}}, StreamOptions{
		Identity: &ClientIdentity{Attribution: &PiAttribution, UserAgent: new(UserAgentShort)},
		Headers:  ProviderHeaders{"HTTP-Referer": nil},
	})
	if got := header.Get("User-Agent"); got != pigidentity.UserAgentProduct {
		t.Errorf("User-Agent = %q, want the short identity", got)
	}
	if got := header.Get("X-OpenRouter-Title"); got != "model-title" {
		t.Errorf("X-OpenRouter-Title = %q, want the model header", got)
	}
	if _, present := header["Http-Referer"]; present {
		t.Errorf("HTTP-Referer = %q, want it removed by the null request header", header.Get("HTTP-Referer"))
	}
	if got := header.Get("X-OpenRouter-Categories"); got != pigidentity.AttributionCategory {
		t.Errorf("X-OpenRouter-Categories = %q", got)
	}
}

func TestIdentityKeepsImpersonatingModelHeadersOverTheIdentityUserAgent(t *testing.T) {
	header := identityRequestHeaders(t, OpenAIConfig{ProviderID: "github-copilot", BaseURL: "https://api.githubcopilot.com", Model: "m", ExtraHeaders: map[string]string{"User-Agent": "GitHubCopilotChat/0.35.0"}}, StreamOptions{
		Identity: &ClientIdentity{UserAgent: new(UserAgentCustom("acme"))},
	})
	if got := header.Get("User-Agent"); got != "GitHubCopilotChat/0.35.0" {
		t.Fatalf("User-Agent = %q, want the model's", got)
	}
}

func TestIdentityNullRequestHeaderRemovesTheUserAgent(t *testing.T) {
	header := identityRequestHeaders(t, OpenAIConfig{ProviderID: "custom", BaseURL: "https://example.test/v1", Model: "m"}, StreamOptions{Headers: ProviderHeaders{"user-agent": nil}})
	if got := header.Values("User-Agent"); len(got) != 0 {
		t.Fatalf("User-Agent = %q, want it removed", got)
	}
}

type identityCaptureModels struct {
	streams  []StreamOptions
	deferred []DeferredFetchOptions
}

func (m *identityCaptureModels) GetModel(provider, id string) *Model { return nil }
func (m *identityCaptureModels) StreamSimple(_ context.Context, _ *Model, _ Context, options ...StreamOptions) *AssistantMessageEventStream {
	m.streams = append(m.streams, options...)
	return nil
}

func (m *identityCaptureModels) CompleteSimple(_ context.Context, _ *Model, _ Context, options ...StreamOptions) *AssistantMessage {
	m.streams = append(m.streams, options...)
	return nil
}

func (m *identityCaptureModels) FetchDeferred(_ context.Context, _ *Model, _ DeferredHandle, options ...DeferredFetchOptions) *AssistantMessage {
	m.deferred = append(m.deferred, options...)
	return nil
}

func (m *identityCaptureModels) CancelDeferred(_ context.Context, _ *Model, _ DeferredHandle, options ...DeferredCancelOptions) error {
	m.streams = append(m.streams, options...)
	return nil
}

type identityFakeCatalogModels struct {
	identityCaptureModels
	classifier []ModelsClassifierOptions
	images     []ModelsImagesOptions
}

func (m *identityFakeCatalogModels) GetModelsOfType(ModelType, ...string) []AnyModel { return nil }
func (m *identityFakeCatalogModels) GetAvailableOfType(context.Context, ModelType, ...string) ([]AnyModel, error) {
	return nil, nil
}
func (m *identityFakeCatalogModels) GetModelOfType(ModelType, string, string) AnyModel { return nil }
func (m *identityFakeCatalogModels) Classify(_ context.Context, _ *ClassifierModel, _ ClassifierContext, options ...ModelsClassifierOptions) ClassifierResult {
	m.classifier = append(m.classifier, options...)
	return ClassifierResult{}
}

func (m *identityFakeCatalogModels) GenerateImages(_ context.Context, _ *ImageModel, _ ImagesContext, options ...ModelsImagesOptions) AssistantImages {
	m.images = append(m.images, options...)
	return AssistantImages{}
}
func (m *identityFakeCatalogModels) GetModels(...string) []*Model { return []*Model{{ID: "listed"}} }

func TestWithClientIdentityMergesRequestIdentityOverTheWrapperIdentity(t *testing.T) {
	capture := &identityCaptureModels{}
	wrapped := WithClientIdentity(capture, ClientIdentity{Attribution: &acmeAttribution, UserAgent: new(UserAgentShort)})
	wrapped.StreamSimple(t.Context(), nil, Context{})
	wrapped.CompleteSimple(t.Context(), nil, Context{}, StreamOptions{Identity: &ClientIdentity{AttributionNull: true}, SessionID: "kept"})
	wrapped.FetchDeferred(t.Context(), nil, DeferredHandle{}, DeferredFetchOptions{StreamOptions: StreamOptions{Identity: &ClientIdentity{Version: new("2")}}})
	if err := wrapped.CancelDeferred(t.Context(), nil, DeferredHandle{}); err != nil {
		t.Fatal(err)
	}
	if len(capture.streams) != 3 || len(capture.deferred) != 1 {
		t.Fatalf("captured %d stream and %d deferred options", len(capture.streams), len(capture.deferred))
	}
	want := []*ClientIdentity{
		{Attribution: &acmeAttribution, UserAgent: new(UserAgentShort)},
		{AttributionNull: true, UserAgent: new(UserAgentShort)},
		{Attribution: &acmeAttribution, UserAgent: new(UserAgentShort)},
	}
	for i, identity := range want {
		if !reflect.DeepEqual(capture.streams[i].Identity, identity) {
			t.Errorf("stream %d identity = %#v, want %#v", i, capture.streams[i].Identity, identity)
		}
	}
	if capture.streams[1].SessionID != "kept" {
		t.Errorf("session id = %q, want kept", capture.streams[1].SessionID)
	}
	if got, want := capture.deferred[0].Identity, (&ClientIdentity{Attribution: &acmeAttribution, UserAgent: new(UserAgentShort), Version: new("2")}); !reflect.DeepEqual(got, want) {
		t.Errorf("deferred identity = %#v, want %#v", got, want)
	}
}

func TestWithClientIdentityKeepsTheOptionalSurfacesItsModelsHave(t *testing.T) {
	plain := WithClientIdentity(&identityCaptureModels{}, ClientIdentity{})
	if _, ok := plain.(ModelsLister); ok {
		t.Error("a models without GetModels gained it")
	}
	if _, ok := plain.(ModelsCatalog); ok {
		t.Error("a models without a catalog gained one")
	}
	catalog := &identityFakeCatalogModels{}
	full := WithClientIdentity(catalog, ClientIdentity{Attribution: &acmeAttribution})
	lister, ok := full.(ModelsLister)
	if !ok || len(lister.GetModels()) != 1 {
		t.Fatalf("lister = %v, %v", lister, ok)
	}
	view, ok := full.(ModelsCatalog)
	if !ok {
		t.Fatal("the wrapped catalog is gone")
	}
	view.Classify(t.Context(), nil, ClassifierContext{})
	view.GenerateImages(t.Context(), nil, ImagesContext{}, ModelsImagesOptions{ImagesOptions: ImagesOptions{Identity: &ClientIdentity{Version: new("3")}}})
	if got, want := catalog.classifier[0].Identity, (&ClientIdentity{Attribution: &acmeAttribution}); !reflect.DeepEqual(got, want) {
		t.Errorf("classifier identity = %#v, want %#v", got, want)
	}
	if got, want := catalog.images[0].Identity, (&ClientIdentity{Attribution: &acmeAttribution, Version: new("3")}); !reflect.DeepEqual(got, want) {
		t.Errorf("images identity = %#v, want %#v", got, want)
	}
}

// models.ts applyAuth: the request identity overrides the Models identity field by field, and a Models without an identity sends the request's alone.
func TestModelsMergeRequestIdentityOverTheModelsIdentity(t *testing.T) {
	var calls []modelsRuntimeCall
	for _, tc := range []struct {
		name    string
		models  *Models
		request *ClientIdentity
		want    *ClientIdentity
	}{
		{"neither", CreateModels(), nil, nil},
		{"models", CreateModels(CreateModelsOptions{Identity: &ClientIdentity{Attribution: &acmeAttribution, UserAgent: new(UserAgentShort)}}), nil, &ClientIdentity{Attribution: &acmeAttribution, UserAgent: new(UserAgentShort)}},
		{"request over models", CreateModels(CreateModelsOptions{Identity: &ClientIdentity{Attribution: &acmeAttribution, UserAgent: new(UserAgentShort)}}), &ClientIdentity{AttributionNull: true}, &ClientIdentity{AttributionNull: true, UserAgent: new(UserAgentShort)}},
		{"request alone", CreateModels(), &ClientIdentity{Version: new("4")}, &ClientIdentity{Version: new("4")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls = nil
			tc.models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p", calls: &calls}))
			model := tc.models.GetModel("p", "model-a")
			tc.models.StreamSimple(t.Context(), model, modelsRuntimeContext(), StreamOptions{Identity: tc.request}).Result()
			if len(calls) != 1 {
				t.Fatalf("%d provider calls", len(calls))
			}
			if got := calls[0].options.Identity; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("identity = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestModelsSendTheOpenCodeSessionHeaderToCustomProvidersPointingAtOpenCode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options StreamOptions
		want    *string
		absent  bool
	}{
		{name: "generated", options: StreamOptions{SessionID: "session-1"}, want: new("session-1")},
		{name: "configured header wins case-insensitively", options: StreamOptions{SessionID: "session-1", Headers: ProviderHeaders{"X-OpenCode-Session": new("configured")}}, want: new("configured")},
		{name: "null header removes it", options: StreamOptions{SessionID: "session-1", Headers: ProviderHeaders{"x-opencode-session": nil}}, absent: true},
		{name: "without a session", options: StreamOptions{}, absent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []modelsRuntimeCall
			model := modelsRuntimeModel("custom-opencode", "model-a")
			model.ProviderMeta.BaseURL = "https://opencode.ai/zen/v1"
			models := CreateModels()
			models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "custom-opencode", models: []*Model{model}, calls: &calls}))
			models.StreamSimple(t.Context(), models.GetModel("custom-opencode", "model-a"), modelsRuntimeContext(), tc.options).Result()
			if len(calls) != 1 {
				t.Fatalf("%d provider calls", len(calls))
			}
			var present *string
			found := false
			for name, value := range calls[0].options.Headers {
				if strings.EqualFold(name, "x-opencode-session") {
					present, found = value, true
				}
			}
			switch {
			case tc.absent && found && present != nil:
				t.Fatalf("session header = %q, want it absent", *present)
			case !tc.absent && (present == nil || *present != *tc.want):
				t.Fatalf("session header = %v, want %q", present, *tc.want)
			}
		})
	}
}

// Every HTTP API sends the identity's User-Agent below its model and request headers (utils/identity.ts getIdentityHeaders).
func TestProviderIdentityUserAgents(t *testing.T) {
	custom := &ClientIdentity{UserAgent: new(UserAgentCustom("acme/1.0"))}
	for _, tc := range userAgentProviderCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			var header http.Header
			provider := tc.build(sseClient(&header, tc.sse))
			opts := StreamOptions{Identity: custom}
			if provider.ID() == string(APIOpenAICodexResponses) {
				opts.Transport = TransportSSE
			}
			drainProviderStream(t, provider, opts)
			if got := header.Get("User-Agent"); got != "acme/1.0" {
				t.Errorf("User-Agent = %q, want the identity's", got)
			}
			if tc.name == "openai-codex-responses" {
				if got := header.Get("originator"); got != pigidentity.CodexOriginator {
					t.Errorf("originator = %q, want %q: the identity does not change it", got, pigidentity.CodexOriginator)
				}
			}
		})
	}
}

func TestCodexWebSocketHandshakeCarriesTheIdentity(t *testing.T) {
	target := identityTarget("openai-codex", "https://chatgpt.com/backend-api")
	headers := codexWebSocketHeaders(target, &ClientIdentity{UserAgent: new(UserAgentShort)}, nil, nil, "token", "acct", "request")
	if headers.Get("User-Agent") != pigidentity.UserAgentProduct || headers.Get("originator") != pigidentity.CodexOriginator {
		t.Fatalf("handshake identity = %q / %q", headers.Get("originator"), headers.Get("User-Agent"))
	}
}

// utils/identity.ts matchesHost is new URL(baseUrl).hostname === host: the WHATWG parser trims, skips and decodes before the comparison.
func TestIdentityHostMatchingFollowsTheWHATWGParser(t *testing.T) {
	for _, tc := range []struct {
		name, baseURL string
		want          bool
	}{
		{"subdomain", "https://zen.opencode.ai/v1", false},
		{"lookalike", "https://notopencode.ai/v1", false},
		{"not a URL", "opencode.ai", false},
		{"invalid port", "https://opencode.ai:99999/v1", false},
		{"non-special scheme keeps case", "foo://OpenCode.ai/v1", false},
		{"any letter case", "HTTPS://OpenCode.AI/zen/v1", true},
		{"leading space", " https://opencode.ai/v1", true},
		{"tab and newline", "\thttps://open\ncode.ai/v1", true},
		{"no slashes after a special scheme", "https:opencode.ai/v1", true},
		{"backslash path", "https://opencode.ai\\v1", true},
		{"userinfo and port", "https://user@opencode.ai:8080/x", true},
		{"percent-encoded host", "https://opencode%2Eai/", true},
		{"special non-http scheme lowercases", "ws://OpenCode.ai", true},
		{"non-special scheme", "foo://opencode.ai/x", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetOpenCodeSessionHeaders(identityTarget("custom", tc.baseURL), "s1") != nil; got != tc.want {
				t.Fatalf("matches = %v, want %v", got, tc.want)
			}
		})
	}
}
