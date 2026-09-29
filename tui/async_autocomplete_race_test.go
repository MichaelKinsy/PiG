package tui

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// The input owner, not the provider worker, applies and renders each result. Cancellation and joined request ordering remain exercised under -race without wall-clock sleeps.
func TestAsyncAutocomplete_NoRaceWithInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newAsyncEditorProbe(t, &AsyncAutocompleteProvider{GetSuggestions: func(ctx context.Context, _ []string, _, _ int, _ bool) (*AutocompleteSuggestions, error) {
			timer := time.NewTimer(2 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "x"}}, Prefix: "/"}, nil
		}})
		for range 300 {
			p.editor.HandleInput("/")
			p.editor.HandleInput("\x7f")
			p.drain()
			_ = p.editor.Render(80)
		}
		p.editor.HandleInput("/")
		synctest.Wait()
		time.Sleep(2 * time.Millisecond)
		p.drain()
		if !p.editor.AutocompleteOpen() {
			t.Fatal("final owned result did not arrive")
		}
	})
}
