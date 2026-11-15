package extensionconformance

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's pi.setModel(model) (core/extensions/loader.ts:453-456 -> agent-session.ts:3583-3587, 2644-2668) takes a Model object: it answers false when
// hasConfiguredAuth(model.provider) is false and otherwise makes that object the session's model, whether or not the registry carries it.
// The NAMED form ("provider/id", a PiG surface Pi has no call for) resolves through BuildModel (coding/session_extension_host.go) and so reaches only a model the registry lists:
// an id nothing lists is the error "Model not found: p/id" and the session keeps its model, in every SDK (the OBJECT form keeps Pi's rule: set_model_object_sdk_test.go, set_model_object_node_test.go).
func TestSetModelObjectAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Setenv("ANTHROPIC_API_KEY", "conformance-key")
	cases := []struct {
		name, spec string
		wantPi     string // "true:<id>" (the session holds the model), "false" (no auth for the provider) or the named form's error
		wantNode   string // Node has no named form: its fixture passes pi.setModel the registry's model or an object it builds by hand, so an unlisted id is Pi's object rule (auth only); empty = wantPi
	}{
		{"builtin-in-catalog", "anthropic/claude-haiku-4-5", "true:claude-haiku-4-5", ""},
		{"builtin-not-in-catalog", "anthropic/claude-handmade-9", "error: Model not found: anthropic/claude-handmade-9", "true:claude-handmade-9"},
		{"builtin-without-credentials", "openai/gpt-5", "false", ""},
		{"unknown-provider", "nonesuch/model-1", "error: Model not found: nonesuch/model-1", "false"},
		{"custom-provider-listed-model", "acme/chat-1", "true:chat-1", ""},
		{"custom-provider-unlisted-model", "acme/chat-2", "error: Model not found: acme/chat-2", "true:chat-2"},
		{"custom-provider-no-models", "acme-empty/anything", "error: Model not found: acme-empty/anything", "true:anything"},
		{"native-provider-listed-model", "native/m1", "true:m1", ""},
		{"native-provider-unlisted-model", "native/m2", "error: Model not found: native/m2", "true:m2"},
		{"oauth-provider-in-catalog", "github-copilot/claude-haiku-4.5", "true:claude-haiku-4.5", ""},
		{"oauth-provider-not-in-catalog", "github-copilot/handmade-1", "error: Model not found: github-copilot/handmade-1", "true:handmade-1"},
	}
	for _, sdk := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(sdk.name, func(t *testing.T) {
			h := sdk.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + sdk.name)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			for name, models := range map[string][]extension.ProviderModelConfig{
				"acme":       {{ID: "chat-1", Name: "Chat 1", Input: []string{"text"}, ContextWindow: 5555, MaxTokens: 100}},
				"acme-empty": nil,
			} {
				if err := services.Registry().RegisterExtensionProvider(name, extension.ProviderConfig{BaseURL: "http://127.0.0.1:9/v1", API: ai.API("openai-completions"), APIKey: "k", Models: models}); err != nil {
					t.Fatal(err)
				}
			}
			native := &ai.Model{ID: "m1", ProviderMeta: ai.ProviderMetadata{ProviderID: "native", API: ai.APIOpenAICompletions}, Capabilities: ai.ModelCapabilities{ContextWindow: 128, MaxOutputTokens: 16}}
			if err := services.ModelRuntime().RegisterNativeProvider(&ai.ModelsProvider{ID: "native", Name: "native", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{native}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "test"}}, nil
			}}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := services.Credentials().Modify(context.Background(), "github-copilot", func(*ai.Credential) (*ai.Credential, error) {
				return &ai.Credential{Type: ai.CredentialOAuth, Access: "tok", Refresh: "ref", Expires: 4102444800000}, nil
			}); err != nil {
				t.Fatal(err)
			}
			start := ai.GetBuiltinModel("anthropic", "claude-sonnet-4-5")
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true, Model: start})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			coding.BindSessionExtensionActions(nil, h.bridge, func() *coding.Session { return session }, extension.ContextActions{}, nil)
			command, ok := findCommand(h.runner, "set_model")
			if !ok {
				t.Skip("no set_model command")
			}
			for _, c := range cases {
				h.ui.ClearRecorded()
				got := ""
				if err := command.Handler(context.Background(), c.spec); err != nil {
					got = "error: " + err.Error()
				} else {
					waitFor(t, func() bool { return len(h.ui.Recorded()) > 0 })
					if strings.HasPrefix(strings.Join(h.ui.Recorded(), ","), "setModel=true") {
						got = "true:" + session.Model().ID
					} else {
						got = "false"
					}
				}
				want := c.wantPi
				if c.wantNode != "" && strings.HasPrefix(sdk.name, "subprocess-node") {
					want = c.wantNode
				}
				t.Logf("%s (%s): %s window=%d", c.name, c.spec, got, session.Model().Capabilities.ContextWindow)
				if got != want {
					t.Errorf("%s (%s): got %q, want %q", c.name, c.spec, got, want)
				}
			}
		})
	}
}
