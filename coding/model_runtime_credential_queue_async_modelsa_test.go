package coding

// pi: packages/coding-agent/src/core/model-runtime.ts

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// model-runtime.ts:580-597 enqueueCredentialOperation chains every credential operation of one provider (login, logout, setRuntimeApiKey, removeRuntimeApiKey) on one FIFO tail whose predecessor failure is swallowed, races an abort against the start only, and keeps other providers' chains apart.
func TestModelRuntimeCredentialOperationsShareOneFIFOQueuePerProvider(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const blocked, other = "anthropic", "openai"
		runtime, _ := loginTestRuntime(t, "")
		release := make(chan struct{})
		loginFailure := errors.New("login prompt failed")
		var wg sync.WaitGroup
		results := map[string]chan error{}
		start := func(name string, op func() error) {
			done := make(chan error, 1)
			results[name] = done
			wg.Go(func() { done <- op() })
			// Every goroutine is durably blocked or finished, so the operation is queued before the next one starts.
			synctest.Wait()
		}
		configured := func(id string) bool { return runtime.GetProviderAuthStatus(id).Configured }
		finished := func(name string) bool { return len(results[name]) == 1 }

		start("login", func() error {
			_, err := runtime.Login(t.Context(), blocked, ai.CredentialAPIKey, ai.AuthInteraction{Prompt: func(ctx context.Context, _ ai.AuthPrompt) (string, error) {
				select {
				case <-release:
					return "", loginFailure
				case <-ctx.Done():
					return "", context.Cause(ctx)
				}
			}})
			return err
		})
		start("set-1", func() error { return runtime.SetRuntimeApiKey(t.Context(), blocked, "k1") })
		start("remove", func() error { return runtime.RemoveRuntimeApiKey(t.Context(), blocked) })
		start("logout", func() error { return runtime.Logout(t.Context(), blocked) })
		start("set-2", func() error { return runtime.SetRuntimeApiKey(t.Context(), blocked, "k2") })
		abandoned, abandon := context.WithCancelCause(t.Context())
		cause := errors.New("caller stopped waiting")
		start("abandoned", func() error { return runtime.SetRuntimeApiKey(abandoned, blocked, "never") })

		// A queued operation waits for the running login: nothing of the blocked provider ran yet.
		for _, name := range []string{"login", "set-1", "remove", "logout", "set-2", "abandoned"} {
			if finished(name) {
				t.Fatalf("%s finished while the first login holds the provider's queue", name)
			}
		}
		if configured(blocked) {
			t.Fatal("a queued SetRuntimeApiKey ran while the first login holds the provider's queue")
		}
		// Another provider has its own chain.
		start("other", func() error { return runtime.SetRuntimeApiKey(t.Context(), other, "other-key") })
		if !finished("other") || !configured(other) {
			t.Fatalf("an operation of another provider waited for the blocked provider: finished=%v configured=%v", finished("other"), configured(other))
		}
		// Cancelling while queued fails that operation at once and does not release the queue.
		abandon(cause)
		synctest.Wait()
		if err := <-results["abandoned"]; !errors.Is(err, cause) {
			t.Fatalf("abandoned operation = %v, want %v", err, cause)
		}
		if finished("login") || finished("set-1") {
			t.Fatal("cancelling a queued operation released the operations ahead of it")
		}
		if configured(blocked) {
			t.Fatal("the cancelled operation ran its task")
		}

		close(release)
		wg.Wait()
		if err := <-results["login"]; !errors.Is(err, loginFailure) {
			t.Fatalf("login = %v, want %v", err, loginFailure)
		}
		// The failed predecessor does not poison the queue: every successor ran, in order, and the cancelled one never did.
		for _, name := range []string{"set-1", "remove", "logout", "set-2", "other"} {
			if err := <-results[name]; err != nil {
				t.Fatalf("%s = %v", name, err)
			}
		}
		auth, err := runtime.GetAuth(t.Context(), blocked)
		if err != nil || auth == nil || auth.Auth.APIKey != "k2" {
			t.Fatalf("key after the queue drained = %+v, %v, want k2 (set-1, remove, logout, set-2 in order; abandoned never ran)", auth, err)
		}
	})
}
