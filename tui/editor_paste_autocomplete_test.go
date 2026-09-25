package tui

import (
	"context"
	"sync/atomic"
	"testing"
)

// countingProvider records GetSuggestions calls and always offers one item so
// any query would open the popup.
type countingProvider struct {
	calls atomic.Int64
}

func (p *countingProvider) GetSuggestions(lines []string, cursorLine, cursorCol int) *AutocompleteSuggestions {
	p.calls.Add(1)
	return &AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "@node_modules/", Label: "node_modules/"}}, Prefix: "@node_modules"}
}

func (p *countingProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) ([]string, int, int) {
	return lines, cursorLine, cursorCol
}

// countingAsyncSource records Suggest calls.
type countingAsyncSource struct {
	calls atomic.Int64
}

func (s *countingAsyncSource) Suggest(ctx context.Context, lines []string, cursorLine, cursorCol int) *AutocompleteSuggestions {
	s.calls.Add(1)
	return &AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "x"}}, Prefix: "x"}
}

// Ports upstream editor.test.ts "does not trigger autocomplete during
// single-line paste": handlePaste cancels autocomplete and inserts without
// querying any provider, so no popup can open after a paste.
func TestEditorPasteDoesNotTriggerAutocomplete(t *testing.T) {
	e := NewEditor()
	provider := &countingProvider{}
	e.SetAutocomplete(provider)
	source := &countingAsyncSource{}
	e.AddAsyncSuggestionSource(source)
	var posted []func()
	e.SetAsyncApply(func(apply func()) { posted = append(posted, apply) })
	provider.calls.Store(0)

	e.HandleInput("\x1b[200~look at @node_modules/react/index.js please\x1b[201~")
	for _, apply := range posted {
		apply()
	}

	if got := e.Text(); got != "look at @node_modules/react/index.js please" {
		t.Fatalf("text = %q", got)
	}
	if got := provider.calls.Load(); got != 0 {
		t.Fatalf("paste queried the autocomplete provider %d times; upstream never does", got)
	}
	if got := source.calls.Load(); got != 0 {
		t.Fatalf("paste queried an async suggestion source %d times; upstream never does", got)
	}
	if len(posted) != 0 {
		t.Fatalf("paste scheduled %d autocomplete applies; upstream schedules none", len(posted))
	}
	if e.AutocompleteOpen() {
		t.Fatal("autocomplete popup is open after a paste")
	}
}

// A paste ending in an @-token is the input that made a delayed Enter accept a
// completion instead of submitting: the paste queued a popup apply, the main
// loop ran it before the Enter arrived, and the Enter was spent on the popup.
func TestEditorPasteEndingInAtTokenLeavesNoPopupForEnter(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(&countingProvider{})
	var posted []func()
	e.SetAsyncApply(func(apply func()) { posted = append(posted, apply) })

	e.HandleInput("\x1b[200~please review @node_modules\x1b[201~")
	// The host loop drains queued applies before the separately-read Enter.
	for _, apply := range posted {
		apply()
	}
	if e.AutocompleteOpen() {
		t.Fatal("popup open when Enter arrives; the Enter would accept a completion instead of submitting")
	}
}

// Upstream handlePaste calls cancelAutocomplete before inserting, so a popup
// that was already open closes when text is pasted.
func TestEditorPasteClosesOpenAutocomplete(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	if !e.AutocompleteOpen() {
		t.Fatal("precondition: typing / should open the slash popup")
	}

	e.HandleInput("\x1b[200~he\x1b[201~")

	if e.AutocompleteOpen() {
		t.Fatal("paste left the autocomplete popup open")
	}
	if got := e.Text(); got != "/he" {
		t.Fatalf("text = %q, want /he", got)
	}
}

// A fast typed slash command name can leave the popup's cached items/prefix
// pinned to an early, shorter prefix if the host only gets a chance to drain
// queued SetAsyncApply callbacks once, early in the burst, and never again
// before Enter arrives. This reproduces that on a Linux parity host: pig's
// input loop (internal/codingagent/interactive_input.go priorityInput)
// always favors newly-arrived terminal input over draining m.uiTaskCh, so a
// whole command typed via one tmux send-keys burst can race ahead of the
// popup's own deferred apply for an arbitrary stretch of keystrokes.
// AutocompleteAccept must not trust that stale cache: it corrupts the
// submitted command by splicing the stale item into the current, longer
// line (e.g. "/probe-oauth-sh" + "probe-clipboard-read" observed in
// production instead of "/probe-oauth-shared").
func TestEditorTypingRaceLeavesStaleAutocompleteForEnter(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider([]SlashCommand{
		{Name: "probe-clipboard-read"},
		{Name: "probe-oauth-shared"},
	}))
	var posted []func()
	e.SetAsyncApply(func(apply func()) { posted = append(posted, apply) })

	const target = "/probe-oauth-shared"
	const hostDrainsAfter = 4 // only the host's one early gap gets serviced, after "/pro"
	for i, ch := range target {
		e.HandleInput(string(ch))
		if i+1 == hostDrainsAfter {
			for _, apply := range posted {
				apply()
			}
			posted = nil
		}
	}
	// No further draining before Enter: the rest of the burst races ahead of
	// the host's uiTaskCh, exactly as on the loaded Linux host.

	if !e.AutocompleteOpen() {
		t.Fatal("precondition: popup should still be open (its cache stuck) when Enter arrives")
	}
	e.AutocompleteAccept()
	if got := e.Text(); got != target+" " {
		t.Fatalf("Enter accepted a stale autocomplete item and corrupted the typed command: got %q, want %q", got, target+" ")
	}
}
