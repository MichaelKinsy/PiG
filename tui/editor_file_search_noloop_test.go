package tui

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
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
// counters and the lines) while the caller reads or types. Time is the bubble's, so the debounce and the search take no wall time.
func TestFileSearchOfAnEditorWithNoLoopIsPublishedByTheCallersGoroutine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := NewEditor()
		e.SetAutocomplete(&slowFileProvider{delay: 30 * time.Millisecond})
		e.HandleInput("@")
		e.HandleInput("k")
		// The caller keeps reading while the debounce and the search run, with nothing of the editor changed under it.
		for range 150 {
			time.Sleep(time.Millisecond)
			_ = e.Text()
			_ = e.GetCursor()
			if e.AutocompleteOpen() {
				t.Fatal("a worker opened the popup")
			}
		}
		// The next render publishes the answer.
		e.Render(40)
		if items := e.autocompleteItems; len(items) != 1 || items[0].Value != "@keep.txt" {
			t.Fatalf("items = %+v, want the one @keep.txt", items)
		}
	})
}

// The mailbox holds one answer, and an older search's answer never replaces a later one: a worker that was cancelled after it passed its checks may stall and store after the search that superseded it.
func TestFileAnswerOfAnOlderSearchNeverReplacesALaterOne(t *testing.T) {
	e := NewEditor()
	var published []uint64
	offer := func(sequence uint64) {
		e.offerFileAnswer(&fileAnswer{sequence: sequence, publish: func() { published = append(published, sequence) }})
	}
	offer(2)
	offer(1)
	e.applyPendingFileAnswer()
	if !slices.Equal(published, []uint64{2}) {
		t.Fatalf("published %v after offering 2 then 1, want [2]", published)
	}
	offer(3)
	offer(4)
	e.applyPendingFileAnswer()
	if !slices.Equal(published, []uint64{2, 4}) {
		t.Fatalf("published %v after offering 3 then 4, want [2 4]", published)
	}
	e.applyPendingFileAnswer()
	if len(published) != 2 {
		t.Fatalf("an answer was published twice: %v", published)
	}
	// Workers that finish in any order leave the latest search's answer.
	var wg sync.WaitGroup
	for sequence := uint64(10); sequence <= 200; sequence++ {
		wg.Go(func() { offer(sequence) })
	}
	wg.Wait()
	e.applyPendingFileAnswer()
	if got := published[len(published)-1]; got != 200 {
		t.Fatalf("the answer left after 191 concurrent offers is search %d, want 200", got)
	}
}
