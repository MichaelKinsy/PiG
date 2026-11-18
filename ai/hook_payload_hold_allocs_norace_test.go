//go:build !race

package ai

import (
	"errors"
	"testing"
)

// hookedRequestAllocs is the allocations of one request that stops in its onPayload hook, the point where a provider has built the payload and handed it out.
func hookedRequestAllocs(t *testing.T, provider Provider) float64 {
	t.Helper()
	captured := errors.New("payload captured")
	transcript := hookedTranscript()
	options := StreamOptions{IsReasoning: true, Thinking: ThinkingLevelMedium, OnPayload: func(any, *Model) (any, error) { return nil, captured }}
	return testing.AllocsPerRun(20, func() {
		if err := requestToHook(t.Context(), provider, transcript, options); !errors.Is(err, captured) {
			t.Fatal(err)
		}
	})
}

// Holding the member order of a payload for the length of its hook costs nothing: a request that stops in its hook allocates what it did on the line before a
// provider held the order (1eede377c47c), which these figures are.
func TestHookedRequestHoldsThePayloadOrderWithoutAllocating(t *testing.T) {
	providers := hookedProviders()
	for name, want := range map[string]float64{"google": 59, "vertex": 72, "pi-messages": 9} {
		if name == "vertex" && !BuiltAPI(APIGoogleVertex) {
			// A build with pig_strip_google_vertex has no Vertex provider (hookedProviders leaves its row out).
			continue
		}
		t.Run(name, func(t *testing.T) {
			if got := hookedRequestAllocs(t, providers[name]); got > want {
				t.Fatalf("a request that stops in its hook allocates %v, want at most %v", got, want)
			}
		})
	}
}

// The hold itself allocates nothing once the registry has grown to the payloads in flight.
func TestHoldHookPayloadAllocatesNothing(t *testing.T) {
	payload := map[string]any{"model": "m"}
	order := &HookObjectOrder{Keys: []string{"model"}}
	hook := func(any, *Model) (any, error) { return nil, nil }
	if got := testing.AllocsPerRun(100, func() { _, _ = callPayloadHook(hook, payload, order, nil) }); got != 0 {
		t.Fatalf("a held payload allocates %v, want 0", got)
	}
}
