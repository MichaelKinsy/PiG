package tui

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

type holdRecord struct{ holds, unholds, ends atomic.Int32 }

func (h *holdRecord) hook(ctx context.Context) (context.Context, func()) {
	h.holds.Add(1)
	return WithAutocompleteUnhold(ctx, func() { h.unholds.Add(1) }), func() { h.ends.Add(1) }
}

func holdEditor(t *testing.T, get func(ctx context.Context, line string) (*AutocompleteSuggestions, error)) (*Editor, func(), *holdRecord, *atomic.Int32) {
	t.Helper()
	e, flush := completionUpstreamEditor(t)
	var workers sync.WaitGroup
	t.Cleanup(workers.Wait)
	record := &holdRecord{}
	calls := &atomic.Int32{}
	lifetime, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	e.SetAsyncAutocomplete(&AsyncAutocompleteProvider{
		GetSuggestions: func(ctx context.Context, lines []string, row, _ int, _ bool) (*AutocompleteSuggestions, error) {
			calls.Add(1)
			return get(ctx, lines[row])
		},
		ApplyCompletion: func(_ context.Context, lines []string, row, col int, _ AutocompleteItem, _ string) ([]string, int, int, error) {
			return lines, row, col, nil
		},
	}, lifetime, workers.Go, func(AutocompleteWork) {}, func(err error) { t.Errorf("autocomplete error: %v", err) })
	e.SetAutocompleteInputHold(record.hook)
	return e, flush, record, calls
}

func answer(line string) (*AutocompleteSuggestions, error) {
	return &AutocompleteSuggestions{Prefix: line, Items: []AutocompleteItem{{Value: line, Label: line}}}, nil
}

// upstream: editor.ts:2286-2338 requestAutocomplete/startAutocompleteRequest. The extension provider chain starts once, at the end of the
// read, on the request of its last key; the host holds its next read from then until the menu is published.
func TestFlushAutocompleteStartsTheExtensionChainOncePerRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush, record, calls := holdEditor(t, func(_ context.Context, line string) (*AutocompleteSuggestions, error) { return answer(line) })
		completionUpstreamType(e, "/mod")
		synctest.Wait()
		if calls.Load() != 0 || record.holds.Load() != 0 {
			t.Fatalf("the chain started before the read ended: %d calls, %d holds", calls.Load(), record.holds.Load())
		}
		e.FlushAutocomplete()
		if record.holds.Load() != 1 {
			t.Fatalf("holds = %d, want 1 at the end of the read", record.holds.Load())
		}
		flush()
		completionUpstreamOpen(t, e, true)
		if calls.Load() != 1 || record.ends.Load() != 1 || record.unholds.Load() != 0 {
			t.Fatalf("calls=%d ends=%d unholds=%d, want 1 call, 1 end after the publish and no I/O announcement", calls.Load(), record.ends.Load(), record.unholds.Load())
		}
	})
}

// A chain that awaits real I/O announces it: the host stops holding input while the request goes on, and the late menu is still published.
func TestExtensionChainAwaitingIOReleasesTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		t.Cleanup(unblock)
		e, flush, record, _ := holdEditor(t, func(ctx context.Context, line string) (*AutocompleteSuggestions, error) {
			AutocompleteAwaitsIO(ctx)
			<-release
			return answer(line)
		})
		completionUpstreamType(e, "/mod")
		e.FlushAutocomplete()
		synctest.Wait()
		if record.unholds.Load() != 1 || record.ends.Load() != 0 {
			t.Fatalf("unholds=%d ends=%d, want the hold released while the request is still running", record.unholds.Load(), record.ends.Load())
		}
		unblock()
		flush()
		completionUpstreamOpen(t, e, true)
		if record.ends.Load() != 1 {
			t.Fatalf("ends = %d after the publish", record.ends.Load())
		}
	})
}

// upstream: editor.ts:2321-2323 await previousTask. A request behind an unfinished one cannot settle in the checkpoint after its read.
func TestExtensionChainBehindAnUnfinishedRequestDoesNotHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		t.Cleanup(unblock)
		e, flush, record, _ := holdEditor(t, func(_ context.Context, line string) (*AutocompleteSuggestions, error) {
			if line == "/a" {
				<-release
			}
			return answer(line)
		})
		completionUpstreamType(e, "/a")
		e.FlushAutocomplete()
		synctest.Wait()
		completionUpstreamType(e, "b")
		e.FlushAutocomplete()
		synctest.Wait()
		if record.holds.Load() != 2 || record.unholds.Load() != 1 {
			t.Fatalf("holds=%d unholds=%d, want the second request released at once", record.holds.Load(), record.unholds.Load())
		}
		unblock()
		flush()
	})
}

// upstream: editor.ts:2305-2312 a symbol-completion token waits out a setTimeout, a later task than any read: nothing to hold for.
func TestDebouncedExtensionRequestDoesNotHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush, record, calls := holdEditor(t, func(_ context.Context, line string) (*AutocompleteSuggestions, error) { return answer(line) })
		completionUpstreamType(e, "see @src")
		e.FlushAutocomplete()
		synctest.Wait()
		completionUpstreamTick(flush, int(attachmentAutocompleteDebounce.Milliseconds())+1)
		if record.holds.Load() != 0 || calls.Load() != 1 {
			t.Fatalf("holds=%d calls=%d, want a debounced request that holds nothing", record.holds.Load(), calls.Load())
		}
	})
}
