package codingagent

import (
	"context"
	"testing"
)

// An extension provider's check runs its own code between Pi's credential reads, and that code can rewrite auth.json: a pass with one keeps a stat per read.
func TestAvailabilityReadContextOnlyWithoutExtensionProviders(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	t.Cleanup(func() { r.CloseModelTasks(); r.NativeModels().Close() })
	ctx := context.Background()
	if r.AvailabilityReadContext(ctx) == ctx {
		t.Fatal("a pass over built-in providers only read without one auth.json view")
	}
	if err := r.RegisterNativeModelsProvider(credentialTestProvider("extension")); err != nil {
		t.Fatal(err)
	}
	if r.AvailabilityReadContext(ctx) != ctx {
		t.Fatal("a pass with an extension provider read through one auth.json view")
	}
}
