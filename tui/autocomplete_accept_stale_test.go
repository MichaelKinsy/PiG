package tui

import "testing"

// TestAutocompleteAcceptRejectsStaleQueryAfterFastKeystrokeBurst closes the
// input-doubling bug behind the parity-reported "/sett" + "settings" /
// "/se" + "settings" corruption (03-settings-filter-theme, ~1-in-5 on a
// 196-core Linux host, never on Mac).
//
// refreshAutocomplete's local-provider result is always applied through
// scheduleAsyncApply (wired in production to InteractiveMode.postUITask,
// interactive.go:1170-1172), which defers the actual
// autocompleteItems/autocompletePrefix update onto the host's main loop
// instead of setting it synchronously. The main loop's own priority check
// (interactive_input.go's priorityInput) deliberately favors an
// already-arrived keystroke over draining that queued apply, to keep typing
// responsive. Under real terminal timing this queued apply almost always
// runs before the next keystroke; under heavy scheduler contention a whole
// burst -- including Enter -- can dispatch first, leaving
// autocompletePrefix/autocompleteQueryCursor reflecting an earlier, shorter
// prefix than what the buffer now actually holds.
//
// AutocompleteAccept used that stale prefix's length to compute where the
// suggestion replaces text (ApplyCompletion's
// `beforePrefix := line[:cursorCol-len(prefix)]`), splicing the full
// suggestion value in after a leftover, correctly-typed fragment instead of
// replacing the whole token -- producing exactly the reported
// prefix-plus-full-word duplication.
//
// AutocompleteAccept now re-resolves the local provider's suggestions
// against the buffer as it stands right now before accepting (no async
// source is installed here), so the accept completes normally against
// "/settings" instead of just being rejected: ApplyCompletion's
// slash-name branch inserts "/" + value + " " (autocomplete.go, "Mirrors
// autocomplete.ts:368-385"), which is what upstream's own confirm handler
// produces once its cache is no longer stale.
func TestAutocompleteAcceptRejectsStaleQueryAfterFastKeystrokeBurst(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider([]SlashCommand{
		{Name: "settings", Description: "Show settings"},
	}))
	var tasks []func()
	e.SetAsyncApply(func(apply func()) { tasks = append(tasks, apply) })

	// Type "/settings" one character at a time -- mirrors a single tmux
	// send-keys -l "/settings" landing as one raw read that StdinBuffer
	// splits into one chunk per character (confirmed correct and
	// undoubled by direct instrumentation of the production read path).
	// Drain every keystroke's queued apply through "/set" (4 characters),
	// then type the remaining "tings" WITHOUT draining their applies.
	// e.insert applies to e.lines/e.cursor synchronously inside HandleInput
	// regardless of any of this, but the popup refresh for those last five
	// keystrokes is left sitting in the queue, the way it would if the main
	// loop kept dispatching already-arrived rawCh keystrokes (ending in
	// Enter) before getting back around to draining uiTaskCh -- exactly the
	// captured Linux trace: raw bytes and editor.Text() were provably
	// correct and undoubled through the very last keystroke, so any
	// corruption has to come from elsewhere touching the buffer at accept
	// time.
	runes := []rune("/settings")
	const drainThrough = 4 // "/set"
	for i, ch := range runes {
		before := len(tasks)
		e.HandleInput(string(ch))
		if i < drainThrough {
			for _, task := range tasks[before:] {
				task()
			}
		}
	}
	if !e.AutocompleteOpen() {
		t.Fatal("expected the \"/set\" suggestion to still be open")
	}
	if got := e.Text(); got != "/settings" {
		t.Fatalf("buffer already wrong before accept: got %q", got)
	}

	// Enter, with the (stale) popup still open.
	e.AutocompleteAccept()

	if got := e.Text(); got != "/settings " {
		t.Fatalf("AutocompleteAccept corrupted the buffer using a stale suggestion: got %q, want the freshly-resolved \"/settings \"", got)
	}
}
