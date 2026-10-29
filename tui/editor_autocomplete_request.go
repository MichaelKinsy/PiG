package tui

// Ports packages/tui/src/components/editor.ts

import (
	"context"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// nativeAutocompleteRequest is a request a keystroke of the terminal read in progress made of the local provider. Pi's requestAutocomplete only schedules startAutocompleteRequest (editor.ts:2286-2338) and the provider runs in the Promise jobs that follow the read, so a later keystroke of the same read supersedes the request before the provider sees it.
type nativeAutocompleteRequest struct {
	force, explicit bool
	// request, cursor and lines are the editor's state when the key asked; a request whose text moved on is dropped.
	request uint64
	cursor  [2]int
	lines   []string
}

// requestNativeAutocomplete is editor.ts requestAutocomplete for the local provider. A natural symbol request waits out Pi's debounce timer on the ordered worker the extension providers use. Every other request waits for FlushAutocomplete: the end of the read that made it is Pi's microtask checkpoint.
func (e *Editor) requestNativeAutocomplete(force, explicit bool) {
	if e.asyncCancel != nil {
		e.asyncCancel()
		e.asyncCancel = nil
	}
	triggers := e.nativeTriggerCharacters()
	lines := slices.Clone(e.lines)
	if debouncedAutocomplete(lines, e.cursor, triggers, force, explicit) {
		// Pi starts a debounced request from setTimeout (editor.ts:2305-2312), a later task than any terminal read.
		e.nativeAutocomplete = nil
		e.requestAutocompleteProvider(&AsyncAutocompleteProvider{TriggerCharacters: triggers, GetSuggestions: e.ownerPreparedSuggestions(e.autocomplete)}, force, explicit)
		return
	}
	e.nativeAutocomplete = &nativeAutocompleteRequest{force: force, explicit: explicit, request: e.autocompleteRequestID, cursor: e.cursor, lines: lines}
	// A host that does not call FlushAutocomplete after each read still starts the request, on its owner loop.
	e.scheduleAsyncApply(e.FlushAutocomplete)
}

// FlushAutocomplete runs the request the keystrokes of the terminal read just dispatched made, on the owner loop. Pi starts a
// request in a Promise job (editor.ts startAutocompleteRequest) and awaits the provider there; a provider that needs no I/O
// (command names, a command's argument completions, a directory read) is answered in the same microtask checkpoint, so the
// menu it opens is already there when the next terminal read arrives (tui.ts handleTerminalInput handles one StdinBuffer
// sequence per call; the keys of one read all see the menu the previous read left). The owner calls FlushAutocomplete once
// after each read, never between its sequences. An awaited argument completer or an fd search still runs on the worker and
// publishes when it settles, as Pi's does once its child process or Promise resolves.
func (e *Editor) FlushAutocomplete() {
	pending := e.nativeAutocomplete
	if pending == nil {
		return
	}
	e.nativeAutocomplete = nil
	if e.forwards() || e.autocomplete == nil || pending.request != e.autocompleteRequestID || e.cursor != pending.cursor || !slices.Equal(e.lines, pending.lines) {
		return
	}
	if e.autocompleteLifetime != nil && e.autocompleteLifetime.Err() != nil {
		return
	}
	lines, row, col := e.autocompleteView()
	result, query := prepareNativeAutocomplete(e.autocomplete, slices.Clone(lines), row, col, pending.force)
	if query == nil && e.autocompleteWorkerIdle() {
		e.publishAutocomplete(pending.force, pending.explicit, pending.cursor, result, nil)
		return
	}
	if query == nil {
		// An earlier request is still on the worker: Pi's next request awaits it (editor.ts:2321-2323).
		query = func(context.Context) (*AutocompleteSuggestions, error) { return result, nil }
	}
	e.requestAutocompleteProvider(&AsyncAutocompleteProvider{
		TriggerCharacters: e.nativeTriggerCharacters(),
		GetSuggestions: func(ctx context.Context, _ []string, _, _ int, _ bool) (*AutocompleteSuggestions, error) {
			return query(ctx)
		},
	}, pending.force, pending.explicit)
}

// autocompleteWorkerIdle reports whether the ordered worker has no earlier request left to join.
func (e *Editor) autocompleteWorkerIdle() bool {
	if e.autocompleteTaskDone == nil {
		return true
	}
	select {
	case <-e.autocompleteTaskDone:
		return true
	default:
		return false
	}
}

func (e *Editor) nativeTriggerCharacters() []string {
	triggers := make([]string, len(e.autocompleteTriggerCharacters))
	for i, r := range e.autocompleteTriggerCharacters {
		triggers[i] = string(r)
	}
	return triggers
}

// ownerPreparedSuggestions answers on the worker after the provider's synchronous callbacks ran on the owner.
func (e *Editor) ownerPreparedSuggestions(provider AutocompleteProvider) func(context.Context, []string, int, int, bool) (*AutocompleteSuggestions, error) {
	return func(ctx context.Context, lines []string, row, col int, force bool) (*AutocompleteSuggestions, error) {
		type prepared struct {
			result *AutocompleteSuggestions
			query  func(context.Context) (*AutocompleteSuggestions, error)
		}
		answer := make(chan *prepared, 1)
		e.scheduleAsyncApply(func() {
			if ctx.Err() != nil {
				answer <- nil
				return
			}
			result, query := prepareNativeAutocomplete(provider, lines, row, col, force)
			answer <- &prepared{result: result, query: query}
		})
		select {
		case p := <-answer:
			if p == nil {
				return nil, ctx.Err()
			}
			if p.query == nil {
				return p.result, nil
			}
			return p.query(ctx)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// prepareNativeAutocomplete runs the provider's synchronous callbacks, which belong to the owner loop. It returns the complete
// answer, or a query for the part Pi awaits off the loop: a Promise-returning argument completer, the fd search. A query
// captured its input and may run on any goroutine.
func prepareNativeAutocomplete(provider AutocompleteProvider, lines []string, row, col int, force bool) (*AutocompleteSuggestions, func(context.Context) (*AutocompleteSuggestions, error)) {
	if planner, ok := provider.(AsyncSuggestionPlanner); ok {
		if prefix, task, ok := planner.SuggestionTask(lines, row, col, force); ok {
			return nil, func(ctx context.Context) (*AutocompleteSuggestions, error) {
				items, err := task(ctx)
				return &AutocompleteSuggestions{Items: items, Prefix: prefix}, err
			}
		}
	}
	var filePrefix string
	var fileTask func(context.Context) []AutocompleteItem
	if files, ok := provider.(AsyncFileSearcher); ok {
		if prefix, task, ok := files.FileSearchTask(lines, row, col); ok {
			filePrefix, fileTask = prefix, task
		}
	}
	result := provider.GetSuggestions(context.Background(), lines, row, col, AutocompleteSuggestionOptions{Force: force})
	if fileTask == nil {
		return result, nil
	}
	return nil, func(ctx context.Context) (*AutocompleteSuggestions, error) {
		items := fileTask(ctx)
		if result != nil {
			items = append(append([]AutocompleteItem(nil), result.Items...), items...)
			filePrefix = result.Prefix
		}
		return &AutocompleteSuggestions{Items: items, Prefix: filePrefix}, ctx.Err()
	}
}

// debouncedAutocomplete is editor.ts getAutocompleteDebounceMs > 0: a natural request at a symbol-completion token.
func debouncedAutocomplete(lines []string, cursor [2]int, triggers []string, force, explicit bool) bool {
	return !explicit && !force && autocompleteTriggered(jsstring.Slice(lines[cursor[0]], 0, cursor[1]), triggers)
}

func bestAutocompleteMatchIndex(items []AutocompleteItem, prefix string) int {
	first := -1
	if prefix != "" {
		for i, item := range items {
			if item.Value == prefix {
				return i
			}
			if first < 0 && strings.HasPrefix(item.Value, prefix) {
				first = i
			}
		}
	}
	return max(0, first)
}
