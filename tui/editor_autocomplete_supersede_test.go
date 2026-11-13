package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

type heldSuggestions struct {
	text    string
	release chan struct{}
}

// blockingSuggestionProvider answers every query only once the test releases it, and announces each query on calls.
func blockingSuggestionProvider(calls chan heldSuggestions) *AsyncAutocompleteProvider {
	return &AsyncAutocompleteProvider{GetSuggestions: func(_ context.Context, lines []string, _, _ int, _ bool) (*AutocompleteSuggestions, error) {
		held := heldSuggestions{text: strings.Join(lines, "\n"), release: make(chan struct{})}
		calls <- held
		<-held.release
		return &AutocompleteSuggestions{Items: []AutocompleteItem{{Value: held.text, Label: held.text}}, Prefix: held.text}, nil
	}}
}

// editor.ts startAutocompleteRequest runs requests one after the other (`await previousTask`, :2333) and isAutocompleteRequestCurrent (:2431-2437) refuses the answer
// of a request that was aborted or replaced: the answer for "/" that arrives after "/a" was typed is never shown, only the answer for "/a".
func TestAutocompleteAnswerOfASupersededRequestIsNeverShown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := make(chan heldSuggestions, 8)
		p := newAsyncEditorProbe(t, blockingSuggestionProvider(calls))
		p.editor.HandleInput("/")
		p.drain()
		first := <-calls
		p.editor.HandleInput("a")
		p.drain()
		select {
		case second := <-calls:
			t.Fatalf("the request for %q started while the one before it was unfinished", second.text)
		default:
		}
		close(first.release)
		p.drain()
		if p.editor.AutocompleteOpen() {
			t.Fatalf("the answer of the superseded request is shown: %v", p.editor.autocompleteItems)
		}
		second := <-calls
		if second.text != "/a" {
			t.Fatalf("the second query asked about %q, want %q", second.text, "/a")
		}
		close(second.release)
		p.drain()
		if !p.editor.AutocompleteOpen() || len(p.editor.autocompleteItems) != 1 || p.editor.autocompleteItems[0].Value != "/a" {
			t.Fatalf("the current answer is not the menu: open=%v items=%v", p.editor.AutocompleteOpen(), p.editor.autocompleteItems)
		}
	})
}

// cancelAutocompleteRequest (editor.ts:2457-2462) aborts the request in flight: its answer, delivered afterwards, opens nothing.
func TestAutocompleteAnswerOfACancelledRequestIsNeverShown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := make(chan heldSuggestions, 8)
		p := newAsyncEditorProbe(t, blockingSuggestionProvider(calls))
		p.editor.HandleInput("/")
		p.drain()
		held := <-calls
		p.editor.AutocompleteCancel()
		close(held.release)
		p.drain()
		if p.editor.AutocompleteOpen() {
			t.Fatalf("the answer of a cancelled request is shown: %v", p.editor.autocompleteItems)
		}
	})
}

// A request that waited behind an unfinished one starts only if it is still the current one (editor.ts:2335 `startToken !== autocompleteStartToken` returns before any query).
// The owner cancels it after its worker queued the start and before the worker looks at the answer: the start closure must refuse, and the provider is never asked.
// Both the closure's answer and the cancelled context are ready to the worker then, and which one it takes is the runtime's choice, so a start that did not refuse is
// seen with certainty only over many rounds.
func TestAutocompleteRequestCancelledBeforeItsStartAsksTheProviderNothing(t *testing.T) {
	for round := range 200 {
		ctx, cancel := context.WithCancel(t.Context())
		var workers sync.WaitGroup
		var asked atomic.Int32
		entered, releaseFirst := make(chan struct{}), make(chan struct{})
		provider := &AsyncAutocompleteProvider{GetSuggestions: func(_ context.Context, lines []string, _, _ int, _ bool) (*AutocompleteSuggestions, error) {
			if strings.Join(lines, "\n") == "/" {
				close(entered)
				<-releaseFirst
				return nil, nil
			}
			asked.Add(1)
			return nil, nil
		}}
		held, proceed := make(chan func()), make(chan struct{})
		var hold atomic.Bool
		e := NewEditor()
		e.SetAsyncApply(func(fn func()) {
			if !hold.Load() {
				// the owner's own turn: the request's start runs at once, as the probe's drain would run it
				fn()
				return
			}
			held <- fn
			<-proceed
		})
		e.SetAsyncAutocomplete(provider, ctx, workers.Go, func(AutocompleteWork) { t.Error("input work was started") }, func(err error) { t.Errorf("autocomplete error: %v", err) })
		e.HandleInput("/")
		<-entered
		e.HandleInput("a")
		hold.Store(true)
		close(releaseFirst)
		start := <-held
		e.AutocompleteCancel()
		start()
		close(proceed)
		workers.Wait()
		cancel()
		if n := asked.Load(); n != 0 {
			t.Fatalf("round %d: a request cancelled before its start asked the provider %d times", round, n)
		}
	}
}
