package coding

// Ports packages/coding-agent/test/sdk-client-identity.test.ts.

// pi: packages/coding-agent/src/core/client-identity.ts

import (
	"os"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type identityCaptureCase struct {
	provider, baseURL, sessionID string
	telemetryDisabled            bool
	providerHeaders              map[string]string
}

// captureIdentityOptions mirrors captureOptions of sdk-client-identity.test.ts: the options the Session's stream function hands a registered provider.
func captureIdentityOptions(t *testing.T, tc identityCaptureCase) ai.StreamOptions {
	t.Helper()
	services := newTestServices(t)
	if tc.telemetryDisabled {
		if err := services.SettingsManager().SetEnableInstallTelemetry(false); err != nil {
			t.Fatal(err)
		}
	}
	var captured ai.StreamOptions
	modelID := tc.provider + "-test-model"
	if err := services.Registry().RegisterExtensionProvider(tc.provider, extension.ProviderConfig{API: ai.APIOpenAICompletions, APIKey: "test-api-key", BaseURL: tc.baseURL, Headers: tc.providerHeaders, StreamSimple: func(_ extension.Model, _ extension.AIContext, raw extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		captured = raw
		stream := ai.NewAssistantMessageEventStream()
		stream.End(&ai.AssistantMessage{API: ai.APIOpenAICompletions, Provider: tc.provider, Model: modelID, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, StopReason: ai.StopReasonStop})
		return stream
	}, Models: []extension.ProviderModelConfig{{ID: modelID, Name: tc.provider + " Test Model", API: ai.APIOpenAICompletions, Input: []string{"text"}, ContextWindow: 128000, MaxTokens: 4096}}}); err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel(tc.provider+"/"+modelID, services)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{Model: model, SessionID: tc.sessionID, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	stream, err := cacheWarmingStreamFn(func() *Session { return session })(t.Context(), model, ai.NormalizeContext(ai.Context{}), ai.StreamOptions{SessionID: session.ID()})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result == nil || result.StopReason != ai.StopReasonStop {
		t.Fatalf("stream result = %+v", result)
	}
	return captured
}

func unsetTelemetryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PI_TELEMETRY", "")
	if err := os.Unsetenv("PI_TELEMETRY"); err != nil {
		t.Fatal(err)
	}
}

func TestSDKIdentifiesPiGWithADetailedUserAgentAndAttributionAsACLIAgent(t *testing.T) {
	unsetTelemetryEnv(t)
	options := captureIdentityOptions(t, identityCaptureCase{provider: "openrouter", baseURL: "https://openrouter.ai/api/v1"})
	want := &ai.ClientIdentity{UserAgent: new(ai.UserAgentDetailed), Version: new(Version), Attribution: &ai.PiAttribution}
	if !reflect.DeepEqual(options.Identity, want) {
		t.Fatalf("identity = %#v, want %#v", options.Identity, want)
	}
	wantHeaders := map[string]string{"HTTP-Referer": "https://github.com/MichaelKinsy/PiG", "X-OpenRouter-Title": "PiG", "X-OpenRouter-Categories": "cli-agent"}
	if got := ai.GetAttributionHeaders(ai.ClientIdentityTarget{Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}, options.Identity); !reflect.DeepEqual(got, wantHeaders) {
		t.Fatalf("attribution headers = %v, want %v", got, wantHeaders)
	}
}

func TestSDKTurnsAttributionOffWhenTelemetryIsDisabled(t *testing.T) {
	unsetTelemetryEnv(t)
	options := captureIdentityOptions(t, identityCaptureCase{provider: "nvidia", baseURL: "https://integrate.api.nvidia.com/v1", telemetryDisabled: true})
	want := &ai.ClientIdentity{UserAgent: new(ai.UserAgentDetailed), Version: new(Version), AttributionNull: true}
	if !reflect.DeepEqual(options.Identity, want) {
		t.Fatalf("identity = %#v, want %#v", options.Identity, want)
	}
	if got := ai.GetAttributionHeaders(ai.ClientIdentityTarget{Provider: "nvidia", BaseURL: "https://integrate.api.nvidia.com/v1"}, options.Identity); len(got) != 0 {
		t.Fatalf("attribution headers = %v, want none", got)
	}
}

func TestSDKTurnsAttributionOffWithPITelemetryZero(t *testing.T) {
	t.Setenv("PI_TELEMETRY", "0")
	options := captureIdentityOptions(t, identityCaptureCase{provider: "openrouter", baseURL: "https://openrouter.ai/api/v1"})
	if options.Identity == nil || !options.Identity.AttributionNull || options.Identity.Attribution != nil {
		t.Fatalf("identity = %#v, want null attribution", options.Identity)
	}
}

func TestSDKAddsOpenCodeSessionHeadersForCustomOpenCodeProviders(t *testing.T) {
	unsetTelemetryEnv(t)
	options := captureIdentityOptions(t, identityCaptureCase{provider: "custom-opencode", baseURL: "https://opencode.ai/zen/v1", sessionID: "opencode-session"})
	if value := options.Headers["x-opencode-session"]; value == nil || *value != "opencode-session" {
		t.Fatalf("headers = %#v, want x-opencode-session opencode-session", options.Headers)
	}
}

func TestSDKLetsConfiguredOpenCodeHeadersOverrideTheSessionHeader(t *testing.T) {
	unsetTelemetryEnv(t)
	options := captureIdentityOptions(t, identityCaptureCase{provider: "opencode", baseURL: "https://opencode.ai/zen/v1", sessionID: "opencode-session", providerHeaders: map[string]string{"x-opencode-session": "configured-session"}})
	if value := options.Headers["x-opencode-session"]; value == nil || *value != "configured-session" {
		t.Fatalf("headers = %#v, want x-opencode-session configured-session", options.Headers)
	}
}
