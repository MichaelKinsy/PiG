package tui

import (
	"context"
	"testing"
	"time"
)

// slowAsyncSource delivers suggestions after a short delay so its
// goroutine completion overlaps concurrent keystroke handling.
type slowAsyncSource struct{}

func (slowAsyncSource) Suggest(ctx context.Context, lines []string, cursorLine, cursorCol int) *AutocompleteSuggestions {
	select {
	case <-time.After(2 * time.Millisecond):
	case <-ctx.Done():
		return nil
	}
	return &AutocompleteSuggestions{
		Items:  []AutocompleteItem{{Value: "x"}},
		Prefix: "/",
	}
}

// TestAsyncAutocomplete_NoRaceWithInput guards the contract that the
// async suggestion worker never mutates editor state or renders from its
// own goroutine: it posts an apply closure that the host runs on the
// single input goroutine. Run with -race: applying or rendering from
// the worker while HandleInput mutates the lock-free editor would race.
func TestAsyncAutocomplete_NoRaceWithInput(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.AddAsyncSuggestionSource(slowAsyncSource{})

	// Single owner goroutine for the editor: it both handles keystrokes
	// and runs posted applies, mirroring the host's main input loop.
	taskCh := make(chan func(), 256)
	e.SetAsyncApply(func(apply func()) {
		select {
		case taskCh <- func() { apply(); _ = e.Render(80) }:
		default:
		}
	})

	drain := func() {
		for {
			select {
			case fn := <-taskCh:
				fn()
			default:
				return
			}
		}
	}

	for range 300 {
		e.HandleInput("/")
		e.HandleInput("\x7f") // backspace
		drain()
	}
	// Let trailing workers post, then drain on this goroutine.
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case fn := <-taskCh:
			fn()
		case <-deadline:
			return
		}
	}
}
