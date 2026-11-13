package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Every full availability pass reads auth.json through one view. Without an extension provider the pass runs no code but the stores', so its
// providers run on the caller; with one (D628) the view is kept and each provider keeps its goroutine, because the provider's code can wait.
func TestAvailabilityReadContextGivesEveryPassOneAuthView(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	t.Cleanup(r.CloseModelTasks)
	ctx := context.Background()
	pass := r.AvailabilityReadContext(ctx)
	if pass == ctx {
		t.Fatal("a pass over built-in providers only read without one auth.json view")
	}
	if !ai.ProvidersRunOnCaller(pass) {
		t.Fatal("a pass over built-in providers only did not run its providers on the caller")
	}
	if err := r.RegisterNativeModelsProvider(credentialTestProvider("extension")); err != nil {
		t.Fatal(err)
	}
	pass = r.AvailabilityReadContext(ctx)
	if pass == ctx {
		t.Fatal("a pass with an extension provider read without one auth.json view")
	}
	if ai.ProvidersRunOnCaller(pass) {
		t.Fatal("a pass with an extension provider ran its providers on the caller")
	}
}
