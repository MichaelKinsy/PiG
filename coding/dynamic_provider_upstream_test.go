package coding

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestDynamicProviderRefreshPreservesHostListenerAndTranscript(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	services := newTestServices(t)
	model, err := BuildModel("anthropic/claude-sonnet-4-5", services)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	before := len(session.Inner().Entries())
	calls := 0
	detach := services.Registry().SetChangeListener(func() { calls++ })
	defer detach()
	services.Registry().RegisterProvider("anthropic", extension.ProviderConfig{BaseURL: "http://localhost:8080/changed"})
	if calls != 1 || session.Model().ProviderMeta.BaseURL != "http://localhost:8080/changed" {
		t.Fatalf("listener=%d model=%+v", calls, session.Model())
	}
	if len(session.Inner().Entries()) != before {
		t.Fatal("refresh appended a model change")
	}
	services.Registry().UnregisterProvider("anthropic")
	if session.Model().ProviderMeta.BaseURL != model.ProviderMeta.BaseURL {
		t.Fatal("unregister did not restore built-in model")
	}
	mustClose := session.Close()
	if mustClose != nil {
		t.Fatal(mustClose)
	}
	retained := session.Model()
	services.Registry().RegisterProvider("anthropic", extension.ProviderConfig{BaseURL: "http://localhost:8080/after-close"})
	if session.Model() != retained {
		t.Fatal("closed Session retained a registry observer")
	}
}

func TestDynamicProviderOverridesActiveSession(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		line       int
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-provider.test.ts:95
		{"applies top-level registerProvider overrides to the active model", "top-level", 95},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-provider.test.ts:108
		{"applies session_start registerProvider overrides to the active model", "session-start", 108},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-provider.test.ts:138
		{"applies command-time registerProvider overrides without reload", "command", 138},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-provider.test.ts:125
		{"registers native pi-ai providers during extension loading", "native-top-level", 125},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-provider.test.ts:159
		{"registers native pi-ai providers at command time", "native-command", 159},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-provider.test.ts; tc.line identifies the corresponding it block.
			t.Logf("upstream case line %d", tc.line)
			t.Setenv("ANTHROPIC_API_KEY", "test-key")
			services := newTestServices(t)
			model, err := BuildModel("anthropic/claude-sonnet-4-5", services)
			if err != nil {
				t.Fatal(err)
			}
			want := "http://localhost:8080/" + tc.path
			register := func() {
				if strings.HasPrefix(tc.path, "native-") {
					catalog, _ := ai.LookupModelExact("anthropic/claude-sonnet-4-5")
					nativeModel := catalog.ToModel()
					nativeModel.ProviderMeta.BaseURL = want
					unused := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
						panic("unused")
					}
					if err := services.Registry().RegisterNativeModelsProvider(&ai.ModelsProvider{ID: "anthropic", Name: "Native Anthropic", BaseURL: want, GetModels: func() ([]*ai.Model, error) { return []*ai.Model{nativeModel}, nil }, Stream: unused, StreamSimple: unused, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Test API key", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
						return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "test-key"}, Source: "test"}, nil
					}}}}); err != nil {
						t.Fatal(err)
					}
				} else {
					services.Registry().RegisterProvider("anthropic", extension.ProviderConfig{BaseURL: want})
				}
			}
			command, description := "use-proxy", "Use proxy"
			if tc.path == "native-command" {
				command, description = "use-native", "Use native provider"
			}
			ext := extension.Extension{Path: "dynamic"}
			if tc.path == "top-level" || tc.path == "native-top-level" {
				register()
			}
			if tc.path == "session-start" {
				ext.Handlers = map[string][]extension.HandlerFn{"session_start": {func(...any) (any, error) { register(); return nil, nil }}}
			}
			if tc.path == "command" || tc.path == "native-command" {
				ext.Commands = map[string]extension.RegisteredCommand{command: {Name: command, Description: description, Handler: func(context.Context, string) error { register(); return nil }}}
			}
			runner := inproc.NewRunner([]extension.Extension{ext}, services.CWD())
			session, err := NewSession(services, SessionOptions{Model: model, Runner: runner, NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			drainSessionEvents(t, session)
			if tc.path == "session-start" {
				session.EmitSessionStart("startup")
			}
			if tc.path == "command" || tc.path == "native-command" {
				if _, err := session.Send(t.Context(), "/"+command); err != nil {
					t.Fatal(err)
				}
			}
			if got := session.Model().ProviderMeta.BaseURL; got != want {
				t.Errorf("active model URL=%q want %q", got, want)
			}
			var sent string
			session.Agent().SetStreamFunction(func(_ context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				sent = model.ProviderMeta.BaseURL
				return nil, errors.New("stop")
			})
			_, _ = session.Send(t.Context(), "hello")
			if sent != want {
				t.Fatalf("prompt model URL=%q want %q", sent, want)
			}
		})
	}
}
