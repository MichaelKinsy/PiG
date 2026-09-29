package tui

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
)

// upstream: packages/tui/src/components/editor.ts:1112-1125,2289-2358
func TestOwnedAutocompleteProgrammaticCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		var calls []string
		e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, _ bool) *AutocompleteSuggestions {
			calls = append(calls, prefix)
			return completionUpstreamItems(prefix, "value")
		}})
		e.SetText("@seed")
		completionUpstreamTick(flush, 20)
		completionUpstreamRequests(t, calls, nil)
		e.HandleInput("x")
		completionUpstreamTick(flush, 19)
		completionUpstreamRequests(t, calls, nil)
		completionUpstreamTick(flush, 1)
		completionUpstreamRequests(t, calls, []string{"@seedx"})
		e.HandleInput("y")
		e.SetText("replacement")
		completionUpstreamTick(flush, 20)
		completionUpstreamRequests(t, calls, []string{"@seedx"})
		completionUpstreamOpen(t, e, false)
	})
}

// upstream: packages/tui/src/components/editor.ts:2311-2333
func TestOwnedAutocompleteJoinsCancelledPredecessors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		var calls []string
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		t.Cleanup(unblock)
		e.SetAutocomplete(NewCombinedProvider([]SlashCommand{{Name: "cmd", AwaitArgumentCompletions: func(prefix string) ([]AutocompleteItem, error) {
			calls = append(calls, prefix)
			if prefix == "a" {
				<-release
			}
			return []AutocompleteItem{{Value: prefix, Label: prefix}}, nil
		}}}, t.TempDir(), ""))
		e.SetText("/cmd ")
		e.HandleInput("a")
		flush()
		completionUpstreamRequests(t, calls, []string{"a"})
		e.HandleInput("b")
		flush()
		e.HandleInput("c")
		flush()
		completionUpstreamRequests(t, calls, []string{"a"})
		unblock()
		flush()
		if !slices.Equal(calls, []string{"a", "abc"}) || len(e.autocompleteItems) != 1 || e.autocompleteItems[0].Value != "abc" {
			t.Fatalf("calls=%q items=%+v, want a then abc and only abc published", calls, e.autocompleteItems)
		}
	})
}

// upstream: packages/tui/src/components/editor.ts:2362-2418
func TestOwnedAutocompleteErrorRunsOnOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		sentinel := errors.New("query failed")
		var reported error
		e.autocompleteError = func(err error) { reported = err }
		e.SetAutocomplete(NewCombinedProvider([]SlashCommand{{Name: "cmd", AwaitArgumentCompletions: func(string) ([]AutocompleteItem, error) { return nil, sentinel }}}, t.TempDir(), ""))
		e.SetText("/cmd ")
		e.HandleInput("x")
		synctest.Wait()
		if reported != nil {
			t.Fatal("error reporter ran before owner dispatch")
		}
		flush()
		if !errors.Is(reported, sentinel) || reported.Error() != sentinel.Error() {
			t.Fatalf("reported=%v, want exact callback error", reported)
		}
	})
}

// upstream: packages/tui/src/components/editor.ts:2311-2333,2424-2440
func TestOwnedAutocompleteWaitsForOwnerPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, _ := completionUpstreamEditor(t)
		tasks := make(chan func(), 8)
		finished := make(chan struct{}, 1)
		start := e.startAutocompleteTask
		e.startAutocompleteTask = func(fn func()) { start(func() { fn(); finished <- struct{}{} }) }
		e.SetAsyncApply(func(fn func()) { tasks <- fn })
		t.Cleanup(func() {
			e.AutocompleteCancel()
			for len(tasks) > 0 {
				(<-tasks)()
			}
		})
		e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, _ bool) *AutocompleteSuggestions { return completionUpstreamItems(prefix, "item") }})
		e.HandleInput("/")
		synctest.Wait()
		select {
		case prepare := <-tasks:
			prepare()
		default:
			t.Fatal("owner preparation was not posted")
		}
		synctest.Wait()
		select {
		case <-finished:
			t.Fatal("request completed before owner publication")
		default:
		}
		select {
		case apply := <-tasks:
			apply()
		default:
			t.Fatal("owner publication was not posted")
		}
		synctest.Wait()
		select {
		case <-finished:
		default:
			t.Fatal("request did not complete after owner publication")
		}
		completionUpstreamOpen(t, e, true)
	})
}

// upstream: packages/tui/src/components/editor.ts:2410-2422,2441-2450
func TestOwnedAutocompleteShutdownRejectsQueuedQuery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		e.SetAutocompleteTaskOwner(ctx, e.startAutocompleteTask, e.autocompleteError)
		called := false
		e.SetAutocomplete(&completionUpstreamProvider{query: func(string, bool) *AutocompleteSuggestions { called = true; return nil }})
		e.HandleInput("/")
		cancel()
		flush()
		if called || e.AutocompleteOpen() {
			t.Fatalf("query after shutdown: called=%v open=%v", called, e.AutocompleteOpen())
		}
	})
}
