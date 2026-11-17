package coding

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi's extension setModel answers false, and changes nothing, when the provider has no configured credentials; otherwise setModel sets state.model before it awaits model_select
// (agent-session.ts:3583-3587, 2644-2663). The owner-loop variant runs only that state mutation inside the dispatcher, so an owner can show the model in the same unit, and awaits
// the handlers on the caller, off the loop.
func TestExtensionSetModelOnMainMutatesInsideTheDispatcherAndAwaitsTheHandlersAfterIt(t *testing.T) {
	services := extensionSetModelServices(t)
	runtime := services.ModelRuntime()
	session, err := NewSession(services, SessionOptions{Model: runtime.GetModel("bystander", "m"), NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	t.Run("a provider without credentials changes nothing", func(t *testing.T) {
		stranger := &ai.Model{ID: "m", ProviderMeta: ai.ProviderMetadata{ProviderID: "nobody"}}
		before := session.Model()
		switched, err := session.ExtensionSetModelOnMain(t.Context(), stranger, func(func() error) error {
			t.Error("a model without credentials reached the owner dispatcher")
			return nil
		})
		if err != nil || switched || session.Model() != before {
			t.Fatalf("switched=%t err=%v model changed=%t, want false, nil, unchanged", switched, err, session.Model() != before)
		}
	})
	t.Run("a nil model is an error", func(t *testing.T) {
		if _, err := session.ExtensionSetModelOnMain(t.Context(), nil, nil); err == nil {
			t.Fatal("setModel(nil) answered no error")
		}
	})
	t.Run("the state changes in the dispatcher and the handlers run after it", func(t *testing.T) {
		var inDispatch atomic.Bool
		entered, release := make(chan struct{}), make(chan struct{})
		session.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{
			"model_select": {func(args ...any) (any, error) {
				if inDispatch.Load() {
					t.Error("model_select ran inside the owner dispatcher")
				}
				close(entered)
				select {
				case <-release:
				case <-args[1].(context.Context).Done():
				}
				return nil, nil
			}},
		}}}, t.TempDir()))
		next := runtime.GetModel("acme", "chat-1")
		dispatched := make(chan func() error)
		result := make(chan error, 1)
		type outcome struct {
			switched bool
			err      error
		}
		done := make(chan outcome, 1)
		go func() {
			switched, err := session.ExtensionSetModelOnMain(t.Context(), next, func(mutate func() error) error {
				dispatched <- mutate
				return <-result
			})
			done <- outcome{switched, err}
		}()
		mutate := <-dispatched
		if session.Model().ID == "chat-1" {
			t.Fatal("the model changed before the owner dispatched the mutation")
		}
		inDispatch.Store(true)
		err := mutate()
		inDispatch.Store(false)
		if err != nil {
			t.Fatal(err)
		}
		if got := session.Model(); got.ID != "chat-1" || got.ProviderMeta.ProviderID != "acme" {
			t.Fatalf("after the dispatched mutation the Session's model is %s/%s, want acme/chat-1", got.ProviderMeta.ProviderID, got.ID)
		}
		result <- nil
		<-entered
		select {
		case got := <-done:
			t.Fatalf("setModel returned %+v while its model_select handler was still replying", got)
		default:
		}
		close(release)
		if got := <-done; got.err != nil || !got.switched {
			t.Fatalf("setModel = %+v, want true, nil", got)
		}
	})
}

// extensionSetModelServices is Services whose registry lists two extension-registered providers with configured credentials: acme/chat-1 and bystander/m.
func extensionSetModelServices(t *testing.T) *AgentSessionServices {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PI_OFFLINE", "")
	dir := t.TempDir()
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	for _, provider := range []struct{ name, model string }{{"acme", "chat-1"}, {"bystander", "m"}} {
		config := extension.ProviderConfig{BaseURL: "http://127.0.0.1:9/v1", API: ai.APIOpenAICompletions, APIKey: "k", Models: []extension.ProviderModelConfig{{ID: provider.model, Name: provider.model, Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100}}}
		if err := services.Registry().RegisterExtensionProvider(provider.name, config); err != nil {
			t.Fatal(err)
		}
	}
	if refreshed := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted || len(refreshed.Errors) != 0 {
		t.Fatalf("refresh=%v", refreshed)
	}
	return services
}
