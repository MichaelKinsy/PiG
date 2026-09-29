package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type asyncEditorProbe struct {
	editor       *Editor
	tasks        chan func()
	errors       []error
	inputPending int
}

func newAsyncEditorProbe(t *testing.T, provider *AsyncAutocompleteProvider) *asyncEditorProbe {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	p := &asyncEditorProbe{editor: NewEditor(), tasks: make(chan func())}
	schedule := func(fn func()) {
		select {
		case p.tasks <- fn:
		case <-ctx.Done():
		}
	}
	report := func(err error) { p.errors = append(p.errors, err) }
	input := func(work AutocompleteWork) {
		p.inputPending++
		workers.Go(func() {
			apply, err := work(ctx)
			schedule(func() {
				p.inputPending--
				if err != nil {
					report(err)
				} else if apply != nil {
					apply()
				}
			})
		})
	}
	p.editor.SetAsyncApply(schedule)
	p.editor.SetAsyncAutocomplete(provider, ctx, workers.Go, input, report)
	t.Cleanup(func() { cancel(); workers.Wait() })
	return p
}

func (p *asyncEditorProbe) drain() {
	for {
		synctest.Wait()
		select {
		case task := <-p.tasks:
			task()
		default:
			return
		}
	}
}

// Pi editor.ts only starts natural requests in slash/trigger contexts, and resets custom triggers when its provider changes.
func TestAsyncAutocompleteNaturalTriggers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		provider := &AsyncAutocompleteProvider{GetSuggestions: func(context.Context, []string, int, int, bool) (*AutocompleteSuggestions, error) {
			calls.Add(1)
			return nil, nil
		}}
		p := newAsyncEditorProbe(t, provider)
		p.editor.HandleInput("plain text$")
		p.drain()
		if calls.Load() != 0 {
			t.Fatalf("plain text queried provider %d times", calls.Load())
		}
		provider.TriggerCharacters = []string{"$"}
		p.editor.Clear()
		p.editor.SetAsyncAutocomplete(provider, p.editor.autocompleteLifetime, p.editor.startAutocompleteTask, p.editor.startAutocompleteInput, p.editor.autocompleteError)
		p.editor.HandleInput("$x")
		if calls.Load() != 0 {
			t.Fatal("trigger request was not debounced")
		}
		time.Sleep(attachmentAutocompleteDebounce)
		p.drain()
		if calls.Load() != 1 {
			t.Fatalf("custom trigger calls=%d, want one debounced request", calls.Load())
		}
	})
}

// Pi editor.ts:2284-2335 awaits suggestions without blocking new input; only synchronous completion and trigger callbacks hold input.
func TestForcedAutocompleteQueryDoesNotHoldInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, cancelled := make(chan struct{}), make(chan struct{})
		provider := &AsyncAutocompleteProvider{GetSuggestions: func(ctx context.Context, _ []string, _, _ int, force bool) (*AutocompleteSuggestions, error) {
			if !force {
				return nil, nil
			}
			close(entered)
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		}}
		p := newAsyncEditorProbe(t, provider)
		p.editor.HandleInput("x")
		p.editor.HandleInput("\t")
		synctest.Wait()
		select {
		case <-entered:
		default:
			t.Fatal("forced query did not start")
		}
		if p.inputPending != 0 {
			t.Fatalf("a pending suggestion Promise holds input: %d", p.inputPending)
		}
		p.editor.HandleInput("y")
		p.drain()
		select {
		case <-cancelled:
		default:
			t.Fatal("typing did not cancel forced query")
		}
		if len(p.errors) != 0 {
			t.Fatalf("cancelled request displayed %v", p.errors)
		}
	})
}

// Pi editor.ts:2425-2430 chooses an exact value, then the first prefix match, before the user accepts an unfiltered argument list.
func TestAsyncAutocompleteSelectsBestPrefixAndAppliesProvider(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &AsyncAutocompleteProvider{
			GetSuggestions: func(_ context.Context, lines []string, _, _ int, _ bool) (*AutocompleteSuggestions, error) {
				_, prefix, _ := strings.Cut(lines[0], " ")
				return &AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "one"}, {Value: "two"}, {Value: "three"}}, Prefix: prefix}, nil
			},
			ApplyCompletion: func(_ context.Context, _ []string, _, _ int, item AutocompleteItem, _ string) ([]string, int, int, error) {
				text := "/argtest " + item.Value
				return []string{text}, 0, len(text), nil
			},
		}
		p := newAsyncEditorProbe(t, provider)
		p.editor.HandleInput("/argtest tw")
		p.drain()
		if p.editor.autocompleteCursor != 1 {
			t.Fatalf("selection=%d, want two", p.editor.autocompleteCursor)
		}
		called := false
		p.editor.AcceptAutocomplete(func(submit bool) {
			called = true
			if submit {
				t.Error("argument completion submitted")
			}
		})
		p.drain()
		if !called || p.editor.Text() != "/argtest two" {
			t.Fatalf("completion=%q called=%t", p.editor.Text(), called)
		}
	})
}

func TestAsyncAutocompleteProviderErrorIsVisible(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newAsyncEditorProbe(t, &AsyncAutocompleteProvider{GetSuggestions: func(context.Context, []string, int, int, bool) (*AutocompleteSuggestions, error) {
			return nil, errors.New("provider exploded")
		}})
		p.editor.HandleInput("/")
		p.drain()
		if len(p.errors) != 1 || p.errors[0].Error() != "provider exploded" {
			t.Fatalf("errors=%v", p.errors)
		}
	})
}
