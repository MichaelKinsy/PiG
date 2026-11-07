package llama

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/coding-agent/test/llama-ui.test.ts (Pi df0d88aac, #11810): the Hugging Face search keeps the browsed model selected when the next search lands.

const (
	searchDown  = "\x1b[B"
	searchEnter = "\r"
)

// pendingSearch is a Hugging Face search opened with each query's results held until the test resolves them.
type pendingSearch struct {
	t        *testing.T
	clock    *manualClock
	view     *LlamaView
	mu       sync.Mutex
	pending  map[string]chan []HuggingFaceModel
	selected chan struct {
		model string
		ok    bool
	}
}

func openSearch(t *testing.T) *pendingSearch {
	t.Helper()
	s := &pendingSearch{t: t, clock: newManualClock(), pending: map[string]chan []HuggingFaceModel{}}
	s.selected = make(chan struct {
		model string
		ok    bool
	}, 1)
	s.view = NewLlamaView(func() {})
	s.view.afterFunc = s.clock.AfterFunc
	search := func(ctx context.Context, query string) ([]HuggingFaceModel, error) {
		s.mu.Lock()
		results := make(chan []HuggingFaceModel, 1)
		s.pending[query] = results
		s.mu.Unlock()
		// The search runs on a goroutine the clock started: it is parked for the clock while it waits for the test, as Sleep parks it.
		s.clock.mu.Lock()
		s.clock.running--
		s.clock.cond.Broadcast()
		s.clock.mu.Unlock()
		defer func() {
			s.clock.mu.Lock()
			s.clock.running++
			s.clock.mu.Unlock()
		}()
		select {
		case found := <-results:
			return found, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	go func() {
		model, ok := s.view.SearchModels(search)
		s.selected <- struct {
			model string
			ok    bool
		}{model, ok}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.view.mu.Lock()
		ready := s.view.inputHandler != nil
		s.view.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search never mounted")
		}
		time.Sleep(200 * time.Microsecond)
	}
	return s
}

// typeText types text and lets the debounce expire, so each query's search starts.
func (s *pendingSearch) typeText(text string) {
	for _, char := range text {
		s.view.HandleInput(string(char))
	}
	s.clock.Advance(500)
	s.clock.Settle()
}

func (s *pendingSearch) press(key string) { s.view.HandleInput(key) }

// finishSearch resolves the search of query with results and waits until they are listed.
func (s *pendingSearch) finishSearch(query string, results []HuggingFaceModel) {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		held := s.pending[query]
		s.mu.Unlock()
		if held != nil {
			held <- results
			break
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("no search pending for %s", query)
		}
		time.Sleep(200 * time.Microsecond)
	}
	// The result handler runs on its own goroutine; requireSelected polls until it has listed the results.
	s.clock.Settle()
}

func (s *pendingSearch) selectedID() string {
	rows := strings.Split(widthx.StripAnsi(strings.Join(s.view.Render(120), "\n")), "\n")
	for _, row := range rows {
		if _, after, ok := strings.Cut(row, "→ "); ok {
			return strings.Fields(after)[0]
		}
	}
	return ""
}

func models(ids ...string) []HuggingFaceModel {
	out := make([]HuggingFaceModel, len(ids))
	for index, id := range ids {
		out[index] = HuggingFaceModel{ID: id, Downloads: 1000}
	}
	return out
}

func (s *pendingSearch) requireSelected(want string) {
	s.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for s.selectedID() != want {
		if time.Now().After(deadline) {
			s.t.Fatalf("selected %q, want %q", s.selectedID(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHuggingFaceSearchKeepsTheBrowsedModelSelectedWhenTheNextSearchLands(t *testing.T) {
	s := openSearch(t)
	first := []string{"qwen/a-GGUF", "qwen/b-GGUF", "qwen/c-GGUF"}
	s.typeText("qw")
	s.finishSearch("qw", models(first...))
	s.typeText("e")
	s.press(searchDown)
	s.press(searchDown)
	s.requireSelected("qwen/c-GGUF")

	s.finishSearch("qwe", models(append([]string{"qwen/new-GGUF"}, first...)...))

	s.requireSelected("qwen/c-GGUF")
	s.press(searchEnter)
	if got := <-s.selected; got.model != "qwen/c-GGUF" || !got.ok {
		t.Fatalf("selected %+v, want qwen/c-GGUF", got)
	}
}

func TestHuggingFaceSearchSelectsTheBestMatchWhenTheUserDidNotBrowse(t *testing.T) {
	s := openSearch(t)
	first := []string{"qwen/a-GGUF", "qwen/b-GGUF", "qwen/c-GGUF"}
	s.typeText("qw")
	s.finishSearch("qw", models(first...))
	s.typeText("e")

	s.finishSearch("qwe", models(append([]string{"qwen/new-GGUF"}, first...)...))

	s.requireSelected("qwen/new-GGUF")
}

func TestHuggingFaceSearchSelectsTheBestMatchWhenTheBrowsedModelIsNotInTheNewResults(t *testing.T) {
	s := openSearch(t)
	s.typeText("qw")
	s.finishSearch("qw", models("qwen/a-GGUF", "qwen/b-GGUF", "qwen/c-GGUF"))
	s.typeText("e")
	s.press(searchDown)

	s.finishSearch("qwe", models("qwen/new-GGUF", "qwen/a-GGUF"))

	s.requireSelected("qwen/new-GGUF")
}
