package tui

import (
	"context"
	"testing"
	"time"
)

// blockingFileProvider is a local provider whose @ search never ends by itself: it reports the text it was built for when it starts and again when its context ends.
type blockingFileProvider struct {
	started chan string
	ended   chan struct{}
}

func (*blockingFileProvider) GetSuggestions(context.Context, []string, int, int, AutocompleteSuggestionOptions) *AutocompleteSuggestions {
	return nil
}

func (*blockingFileProvider) ApplyCompletion(lines []string, row, col int, _ AutocompleteItem, _ string) ([]string, int, int) {
	return lines, row, col
}

func (p *blockingFileProvider) FileSearchTask(lines []string, row, col int) (string, func(context.Context) []AutocompleteItem, bool) {
	before := lines[row][:col]
	if len(before) == 0 || before[0] != '@' {
		return "", nil, false
	}
	return before, func(ctx context.Context) []AutocompleteItem {
		p.started <- before
		<-ctx.Done()
		p.ended <- struct{}{}
		return nil
	}, true
}

// An editor with no owner loop and no task owner (the extension view editor) runs its @ search on a goroutine that applies its own answer. Whatever the worker does, it must read no editor state of its own:
// the keys come from another goroutine. Under -race this fails when a search reads e.lines, e.cursor or the request counters while the caller types. The apply of a search that completes writes the editor from
// the worker; that older race is not exercised here, since the search below only ends by being cancelled.
func TestFileSearchOfAnEditorWithNoLoopReadsNoEditorStateOnItsWorker(t *testing.T) {
	provider := &blockingFileProvider{started: make(chan string, 4), ended: make(chan struct{}, 4)}
	e := NewEditor()
	e.SetAutocomplete(provider)
	for _, key := range []string{"@", "a", "l"} {
		e.HandleInput(key)
	}
	select {
	case before := <-provider.started:
		if before != "@al" {
			t.Fatalf("the search started for %q, want the text the last key left, %q", before, "@al")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the search never started")
	}
	// The search has started: a key that triggers nothing supersedes it, and its answer is dropped.
	e.HandleInput("é")
	select {
	case <-provider.ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the started search was not cancelled by a key that triggers nothing")
	}
	// A search made by the next keys is cancelled by the one after it before it starts, and a key that triggers nothing cancels the last.
	e.HandleInput("l")
	e.HandleInput("l")
	e.HandleInput(" ")
	select {
	case before := <-provider.started:
		t.Fatalf("a search started for %q after a key that triggers nothing cancelled the one before it", before)
	case <-time.After(100 * time.Millisecond):
	}
	if e.AutocompleteOpen() {
		t.Fatal("a popup is open though no search answered")
	}
}
