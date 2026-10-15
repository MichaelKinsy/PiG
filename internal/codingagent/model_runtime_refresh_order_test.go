package codingagent

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// models.ts:586-597 sets each failure in the errors Map when that provider's refresh settles, so RefreshModelRuntime reports failures in completion
// order: neither registration order nor any sorted order.
func TestRefreshModelRuntimeReportsFailuresInCompletionOrder(t *testing.T) {
	for _, first := range []string{"openai", "anthropic"} {
		t.Run(first+" fails first", func(t *testing.T) {
			clearAllAuthEnv(t)
			t.Setenv("PI_OFFLINE", "1")
			registry, _, store := radiusTestRegistry(t, `{"providers":{"openai":{"baseUrl":"https://one.invalid","oauth":"radius"},"anthropic":{"baseUrl":"https://two.invalid","oauth":"radius"}}}`, nil)
			synctest.Test(t, func(t *testing.T) {
				ordered := &catalogErrorOrderStore{InMemoryModelsStore: store, started: make(chan string, 2), release: map[string]chan struct{}{"openai": make(chan struct{}), "anthropic": make(chan struct{})}}
				registry.SetModelsStore(ordered)
				done := make(chan ai.ModelsRefreshResult, 1)
				go func() { done <- registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{}) }()
				<-ordered.started
				<-ordered.started
				second := "anthropic"
				if first == "anthropic" {
					second = "openai"
				}
				close(ordered.release[first])
				synctest.Wait()
				close(ordered.release[second])
				result := <-done
				if !slices.Equal(result.ErrorOrder, []string{first, second}) || result.Aborted {
					t.Fatalf("RefreshModelRuntime = %+v, want the failures in completion order [%s %s]", result, first, second)
				}
			})
		})
	}
}
