package tui

import "testing"

func disposeProbe(t *testing.T) (*ModelSelectorComponent, *int) {
	t.Helper()
	ms := NewStaticModelSelectorComponent("Select model", nil, mkItems("openai/gpt-4o", "groq/llama-3"), "")
	cancels := new(int)
	ms.SetRefreshCancel(func() { *cancels++ })
	return ms, cancels
}

// dispose() stops the refresh once and later calls do nothing: model-selector.ts:224-229 (closed guard, clearTimeout, refreshAbortController.abort()).
func TestModelSelectorDisposeCancelsTheRefreshOnce(t *testing.T) {
	ms, cancels := disposeProbe(t)
	ms.Dispose()
	ms.Dispose()
	if *cancels != 1 {
		t.Fatalf("refresh cancelled %d times, want 1", *cancels)
	}
}

// Every way the selector closes disposes it first: cancel (model-selector.ts:393-396), save as default (:398-405) and select (:413-416).
func TestModelSelectorClosingDisposesTheRefresh(t *testing.T) {
	for _, tc := range []struct{ name, key string }{
		{"select", "\r"},
		{"cancel", "\x1b"},
		{"save as default", "\x13"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms, cancels := disposeProbe(t)
			ms.HandleInput(tc.key)
			if !ms.Done() {
				t.Fatalf("%s did not finish the selector", tc.name)
			}
			if *cancels != 1 {
				t.Fatalf("%s cancelled the refresh %d times, want 1", tc.name, *cancels)
			}
		})
	}
	t.Run("a key that keeps the selector open keeps the refresh", func(t *testing.T) {
		ms, cancels := disposeProbe(t)
		ms.HandleInput("g")
		if ms.Done() || *cancels != 0 {
			t.Fatalf("done=%v cancels=%d after a search key", ms.Done(), *cancels)
		}
	})
}
