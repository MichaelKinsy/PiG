//go:build !race

package ai

import (
	"context"
	"testing"
)

// credentialIn does not wrap a context that cannot end in context.WithoutCancel: the wrapper and the Value lookup through it were two of the five allocations of every Background read
// (BenchmarkAuthStorageGetProviderEnv was 5, its entry is 3). A context that can end still takes the wrapper, so it allocates more.
func TestCredentialInSkipsTheCancelWrapperForAContextThatCannotEnd(t *testing.T) {
	store := newCredentialContextStore(t)
	live := t.Context()
	read := func(ctx context.Context) float64 {
		return testing.AllocsPerRun(200, func() { _, _, _ = store.credentialIn(ctx, "custom") })
	}
	background, cancelable := read(context.Background()), read(live)
	if background > 3 {
		t.Fatalf("a Background read allocates %v times, want at most 3 (the path bytes and the env map clone's two)", background)
	}
	if cancelable != background+2 {
		t.Fatalf("a cancelable read allocates %v times against %v for Background, want 2 more (the wrapper and its Value lookup)", cancelable, background)
	}
}
