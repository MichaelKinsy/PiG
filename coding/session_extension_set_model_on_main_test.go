package coding

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's extension setModel answers false, and changes nothing, when the provider has no configured credentials; otherwise setModel sets state.model before it awaits model_select
// (agent-session.ts:3583-3587, 2644-2663). The owner-loop variant runs only that state mutation inside the dispatcher, so an owner can show the model in the same unit, and awaits
// the handlers on the caller, off the loop.
func TestExtensionSetModelOnMainMutatesInsideTheDispatcherAndAwaitsTheHandlersAfterIt(t *testing.T) {
	services, _ := modelObjectRefreshServices(t)
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
		ctx := testbudget.Context(t)
		session.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{
			"model_select": {func(args ...any) (any, error) {
				if inDispatch.Load() {
					t.Error("model_select ran inside the owner dispatcher")
					return nil, nil
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
			switched, err := session.ExtensionSetModelOnMain(ctx, next, func(mutate func() error) error {
				select {
				case dispatched <- mutate:
				case <-ctx.Done():
					return ctx.Err()
				}
				select {
				case err := <-result:
					return err
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			done <- outcome{switched, err}
		}()
		var mutate func() error
		select {
		case mutate = <-dispatched:
		case <-entered:
			t.Fatal("the model_select handler ran before the owner dispatched the state mutation")
		case got := <-done:
			t.Fatalf("setModel returned %+v without handing its state mutation to the owner dispatcher", got)
		case <-ctx.Done():
			t.Fatal("the Session never handed its state mutation to the owner dispatcher")
		}
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
		select {
		case <-entered:
		case got := <-done:
			t.Fatalf("setModel returned %+v before its model_select handler ran", got)
		case <-ctx.Done():
			t.Fatal("the model_select handler never ran after the owner dispatched the state mutation")
		}
		select {
		case got := <-done:
			t.Fatalf("setModel returned %+v while its model_select handler was still replying", got)
		default:
		}
		close(release)
		select {
		case got := <-done:
			if got.err != nil || !got.switched {
				t.Fatalf("setModel = %+v, want true, nil", got)
			}
		case <-ctx.Done():
			t.Fatal("setModel never returned after its model_select handler replied")
		}
	})
}
