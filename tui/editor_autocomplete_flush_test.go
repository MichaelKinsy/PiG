package tui

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
)

// upstream: packages/tui/src/components/editor.ts:2286-2338 (requestAutocomplete schedules startAutocompleteRequest; only the
// newest request's token survives its first await, so a provider sees the last request of a read once)
func TestFlushAutocompleteAnswersTheLastRequestOfAReadOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		var calls []string
		e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, _ bool) *AutocompleteSuggestions {
			calls = append(calls, prefix)
			return completionUpstreamItems(prefix, "/model")
		}})
		completionUpstreamType(e, "/mod")
		synctest.Wait()
		completionUpstreamRequests(t, calls, nil)
		completionUpstreamOpen(t, e, false)
		e.FlushAutocomplete()
		completionUpstreamRequests(t, calls, []string{"/mod"})
		completionUpstreamOpen(t, e, true)
		// The task each key posted for an owner that never calls FlushAutocomplete finds nothing left to run.
		flush()
		completionUpstreamRequests(t, calls, []string{"/mod"})
		completionUpstreamOpen(t, e, true)
	})
}

// An owner that does not flush after each read still gets the menu: the request each key posted starts it on the owner loop.
func TestPostedRequestStartsWithoutAFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		var calls []string
		e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, _ bool) *AutocompleteSuggestions {
			calls = append(calls, prefix)
			return completionUpstreamItems(prefix, "/model")
		}})
		completionUpstreamType(e, "/mod")
		completionUpstreamOpen(t, e, false)
		flush()
		completionUpstreamRequests(t, calls, []string{"/mod"})
		completionUpstreamOpen(t, e, true)
	})
}

// upstream: editor.ts:2380-2382 isAutocompleteRequestCurrent: an answer for text the editor no longer holds is not published
func TestFlushAutocompleteDropsARequestTheEditorMovedOnFrom(t *testing.T) {
	for name, move := range map[string]func(*Editor){
		"text replaced":  func(e *Editor) { e.SetText("plain") },
		"menu cancelled": func(e *Editor) { e.AutocompleteCancel() },
		"cursor moved":   func(e *Editor) { e.HandleInput("\x1b[D") },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, flush := completionUpstreamEditor(t)
				var calls []string
				e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, _ bool) *AutocompleteSuggestions {
					calls = append(calls, prefix)
					return completionUpstreamItems(prefix, "/model")
				}})
				completionUpstreamType(e, "/mo")
				move(e)
				e.FlushAutocomplete()
				flush()
				completionUpstreamRequests(t, calls, nil)
				completionUpstreamOpen(t, e, false)
			})
		})
	}
}

// upstream: editor.ts:2353-2361 getAutocompleteDebounceMs: a natural symbol request starts from setTimeout, a later task than the read
func TestFlushAutocompleteLeavesTheSymbolDebounceToItsTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		var calls []string
		e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, _ bool) *AutocompleteSuggestions {
			calls = append(calls, prefix)
			return completionUpstreamItems(prefix, "@src/")
		}})
		completionUpstreamType(e, "@sr")
		e.FlushAutocomplete()
		synctest.Wait()
		completionUpstreamRequests(t, calls, nil)
		completionUpstreamTick(flush, 19)
		completionUpstreamRequests(t, calls, nil)
		completionUpstreamOpen(t, e, false)
		completionUpstreamTick(flush, 1)
		completionUpstreamRequests(t, calls, []string{"@sr"})
		completionUpstreamOpen(t, e, true)
	})
}

// upstream: editor.ts:2362-2373 await getSuggestions: a Promise-returning argument completer settles on a later turn, so its
// menu is not open at the end of the read; it publishes when the worker's answer reaches the owner
func TestFlushAutocompleteLeavesAnAwaitedAnswerToTheWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		var mu sync.Mutex
		var calls []string
		e.SetAutocomplete(NewCombinedProvider([]SlashCommand{{Name: "cmd", AwaitArgumentCompletions: func(prefix string) ([]AutocompleteItem, error) {
			mu.Lock()
			calls = append(calls, prefix)
			mu.Unlock()
			return []AutocompleteItem{{Value: prefix + "!", Label: prefix + "!"}}, nil
		}}}, t.TempDir(), ""))
		e.SetText("/cmd ")
		completionUpstreamType(e, "ab")
		e.FlushAutocomplete()
		completionUpstreamOpen(t, e, false)
		flush()
		completionUpstreamOpen(t, e, true)
		mu.Lock()
		defer mu.Unlock()
		completionUpstreamRequests(t, calls, []string{"ab"})
	})
}

// upstream: editor.ts:2321-2323 await previousTask: a request made while an earlier one is still on the worker waits for it,
// so it is not answered at the end of its read
func TestFlushAutocompleteJoinsARequestStillOnTheWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		t.Cleanup(unblock)
		e.SetAutocomplete(NewCombinedProvider([]SlashCommand{{Name: "cmd", AwaitArgumentCompletions: func(prefix string) ([]AutocompleteItem, error) {
			if prefix == "a" {
				<-release
			}
			return []AutocompleteItem{{Value: prefix, Label: prefix}}, nil
		}}}, t.TempDir(), ""))
		e.SetText("/cmd ")
		e.HandleInput("a")
		e.FlushAutocomplete()
		flush()
		completionUpstreamOpen(t, e, false)
		// Back to the command name: a request that needs no I/O, behind one that is not finished.
		e.HandleInput("\x7f")
		e.HandleInput("\x7f")
		completionUpstreamText(t, e, "/cmd")
		e.FlushAutocomplete()
		flush()
		completionUpstreamOpen(t, e, false)
		unblock()
		flush()
		completionUpstreamOpen(t, e, true)
	})
}

// The provider's lifetime ends the request: a read that ends after shutdown starts nothing.
func TestFlushAutocompleteAfterShutdownStartsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := NewEditor()
		ctx, cancel := context.WithCancel(t.Context())
		var workers sync.WaitGroup
		e.SetAsyncApply(func(func()) {})
		e.SetAutocompleteTaskOwner(ctx, workers.Go, func(err error) { t.Errorf("autocomplete error: %v", err) })
		called := false
		e.SetAutocomplete(&completionUpstreamProvider{query: func(string, bool) *AutocompleteSuggestions { called = true; return nil }})
		e.HandleInput("/")
		cancel()
		e.FlushAutocomplete()
		workers.Wait()
		if called {
			t.Fatal("the provider ran after the editor's lifetime ended")
		}
	})
}
