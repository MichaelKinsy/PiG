package tui

import (
	"context"
	"testing"
	"time"
)

// slowFileProvider is a local provider whose @ search takes a while and answers one item.
type slowFileProvider struct{ delay time.Duration }

func (*slowFileProvider) GetSuggestions(context.Context, []string, int, int, AutocompleteSuggestionOptions) *AutocompleteSuggestions {
	return nil
}

func (*slowFileProvider) ApplyCompletion(lines []string, row, col int, _ AutocompleteItem, _ string) ([]string, int, int) {
	return lines, row, col
}

func (p *slowFileProvider) FileSearchTask(lines []string, row, col int) (string, func(context.Context) []AutocompleteItem, bool) {
	before := lines[row][:col]
	if len(before) == 0 || before[0] != '@' {
		return "", nil, false
	}
	return before, func(ctx context.Context) []AutocompleteItem {
		select {
		case <-time.After(p.delay):
			return []AutocompleteItem{{Value: "@keep.txt", Label: "keep.txt"}}
		case <-ctx.Done():
			return nil
		}
	}, true
}

// An editor with no owner loop and no task owner has no goroutine it may publish on: Pi applies a search's answer on its one event loop. The search runs on a worker, which touches no editor state and
// leaves its answer in a mailbox for the caller's goroutine, where the next render or key publishes it. Under -race this fails when the worker applies the answer itself (writes the popup, reads the request
// counters and the lines) while the caller reads or types.
func TestFileSearchOfAnEditorWithNoLoopIsPublishedByTheCallersGoroutine(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(&slowFileProvider{delay: 30 * time.Millisecond})
	e.HandleInput("@")
	e.HandleInput("k")
	// The caller keeps reading while the debounce and the search run, with nothing of the editor changed under it.
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = e.Text()
		_ = e.GetCursor()
		if e.AutocompleteOpen() {
			t.Fatal("a worker opened the popup")
		}
		time.Sleep(time.Millisecond)
	}
	// The next render publishes the answer.
	e.Render(40)
	if items := e.autocompleteItems; len(items) != 1 || items[0].Value != "@keep.txt" {
		t.Fatalf("items = %+v, want the one @keep.txt", items)
	}
}
