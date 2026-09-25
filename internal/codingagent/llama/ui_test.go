package llama

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	keyDown  = "\x1b[B"
	keyEnter = "\r"
	keyEsc   = "\x1b"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m|\x1b\]8;;[^\x07]*\x07`)

func plain(view *LlamaView) string {
	return ansiPattern.ReplaceAllString(strings.Join(view.Render(100), "\n"), "")
}

// waitFor polls the rendered view until it contains every want.
func waitFor(t *testing.T, view *LlamaView, want ...string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		text := plain(view)
		missing := ""
		for _, fragment := range want {
			if !strings.Contains(text, fragment) {
				missing = fragment
				break
			}
		}
		if missing == "" {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("view never showed %q; last render:\n%s", missing, text)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func result[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("flow did not return")
	}
	var zero T
	return zero
}

func TestLlamaViewShowModelsSortsDescribesAndSelects(t *testing.T) {
	view := NewLlamaView(func() {})
	if text := plain(view); !strings.Contains(text, "llama.cpp models") || !strings.Contains(text, "Loading…") {
		t.Fatalf("initial view = %q", text)
	}
	nCtx := 32768.0
	models := []LlamaModelInfo{
		{ID: "zeta", Status: LlamaModelInfoStatus{Value: LlamaModelStatusUnloaded}},
		{ID: "beta", Status: LlamaModelInfoStatus{Value: LlamaModelStatusLoading}},
		{ID: "alpha", Status: LlamaModelInfoStatus{Value: LlamaModelStatusLoaded}, Meta: &LlamaModelMeta{NCtx: &nCtx}},
		{ID: "gamma", Status: LlamaModelInfoStatus{Value: LlamaModelStatusSleeping, Args: []string{"-c", "8192"}}},
	}
	actions := make(chan LlamaManagerAction, 1)
	go func() { actions <- view.ShowModels("http://127.0.0.1:8080", models) }()
	text := waitFor(t, view, "http://127.0.0.1:8080", "Download model…")
	order := []string{"alpha", "beta", "gamma", "zeta", "Download model…"}
	last := -1
	for _, id := range order {
		index := strings.Index(text, id)
		if index < last {
			t.Fatalf("model order wrong at %q:\n%s", id, text)
		}
		last = index
	}
	for _, fragment := range []string{"loaded · 33k context", "loading", "loaded · 8k context", "Hugging Face owner/repository[:quant]", "load/unload/download", "close"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("view is missing %q:\n%s", fragment, text)
		}
	}
	view.HandleInput(keyDown)
	view.HandleInput(keyEnter)
	if action := result(t, actions); action.Type != LlamaManagerActionModel || action.Model.ID != "beta" {
		t.Fatalf("action = %+v, want beta", action)
	}

	go func() { actions <- view.ShowModels("url", nil) }()
	waitFor(t, view, "url", "Download model…")
	view.HandleInput(keyEnter)
	if action := result(t, actions); action.Type != LlamaManagerActionDownload {
		t.Fatalf("action = %+v, want download", action)
	}
	go func() { actions <- view.ShowModels("url2", nil) }()
	waitFor(t, view, "url2")
	view.HandleInput(keyEsc)
	if action := result(t, actions); action.Type != LlamaManagerActionClose {
		t.Fatalf("action = %+v, want close", action)
	}
}

func TestLlamaViewSelectConfirmAndConnectionError(t *testing.T) {
	view := NewLlamaView(func() {})
	answers := make(chan bool, 1)
	go func() { answers <- view.Confirm("Unload model?", "alpha") }()
	waitFor(t, view, "Unload model?", "alpha", "Yes", "No", "select", "cancel")
	view.HandleInput(keyEnter)
	if !result(t, answers) {
		t.Fatal("Confirm = false, want true for Yes")
	}
	go func() { answers <- view.Confirm("Stop?", "x") }()
	waitFor(t, view, "Stop?")
	view.HandleInput(keyEsc)
	if result(t, answers) {
		t.Fatal("cancelled Confirm = true")
	}

	choices := make(chan string, 1)
	go func() { choices <- view.ConnectionError("http://h:1", "Could not connect to the server.") }()
	waitFor(t, view, "llama.cpp unavailable", "http://h:1", "Could not connect to the server.", "Retry", "Close")
	view.HandleInput(keyEnter)
	if got := result(t, choices); got != "retry" {
		t.Fatalf("ConnectionError = %q, want retry", got)
	}
	go func() { choices <- view.ConnectionError("http://h:1", "second failure") }()
	waitFor(t, view, "second failure")
	view.HandleInput(keyEsc)
	if got := result(t, choices); got != "close" {
		t.Fatalf("cancelled ConnectionError = %q, want close", got)
	}
}

func TestRunWithProgressShowsProgressAndCancelsOnConfirmedStop(t *testing.T) {
	view := NewLlamaView(func() {})
	var cancelled atomic.Bool
	type outcome struct {
		cancelled bool
		err       error
	}
	outcomes := make(chan outcome, 1)
	go func() {
		_, stopped, err := RunWithProgress(view, RunWithProgressOptions[int]{
			Title: "Loading model", Model: "alpha", InitialMessage: "Starting…",
			CancelTitle: "Stop loading?", CancelMessage: "alpha",
			Run: func(ctx context.Context, update func(LlamaProgress)) (int, error) {
				half := 0.5
				update(LlamaProgress{Message: "Downloading model", Ratio: &half, Detail: "1 B / 2 B", keys: progressRatioKey | progressDetailKey})
				update(LlamaProgress{Message: "Still going"})
				<-ctx.Done()
				return 0, context.Cause(ctx)
			},
			Cancel: func() error {
				cancelled.Store(true)
				return nil
			},
		})
		outcomes <- outcome{stopped, err}
	}()
	text := waitFor(t, view, "Loading model", "alpha", "Still going", "50%", "1 B / 2 B", "stop")
	if !strings.Contains(text, strings.Repeat("█", 20)+strings.Repeat("─", 20)) {
		t.Fatalf("progress bar missing:\n%s", text)
	}
	view.HandleInput(keyEsc)
	waitFor(t, view, "Stop loading?")
	view.HandleInput(keyDown)
	view.HandleInput(keyEnter)
	waitFor(t, view, "Still going")
	view.HandleInput(keyEsc)
	waitFor(t, view, "Stop loading?")
	view.HandleInput(keyEnter)
	got := result(t, outcomes)
	if !got.cancelled || got.err != nil || !cancelled.Load() {
		t.Fatalf("outcome = %+v, cancel called %v", got, cancelled.Load())
	}
}

func TestRunWithProgressReturnsRunResultAndError(t *testing.T) {
	view := NewLlamaView(func() {})
	value, cancelled, err := RunWithProgress(view, RunWithProgressOptions[string]{
		Run: func(context.Context, func(LlamaProgress)) (string, error) { return "done", nil },
	})
	if value != "done" || cancelled || err != nil {
		t.Fatalf("RunWithProgress = %q, %v, %v", value, cancelled, err)
	}
	failure := errors.New("Model failed to load")
	if _, _, err := RunWithProgress(view, RunWithProgressOptions[string]{
		Run: func(context.Context, func(LlamaProgress)) (string, error) { return "", failure },
	}); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
}

func TestHuggingFaceSearchDebouncesFiltersCachesAndSelects(t *testing.T) {
	view := NewLlamaView(func() {})
	var searches atomic.Int32
	search := func(ctx context.Context, query string) ([]HuggingFaceModel, error) {
		searches.Add(1)
		if query == "err" {
			return nil, errors.New("Hugging Face rate limit reached")
		}
		return []HuggingFaceModel{{ID: "owner/qwen-GGUF", Downloads: 1_250_000}, {ID: "other/llama-GGUF", Downloads: 950}}, nil
	}
	selections := make(chan string, 1)
	go func() {
		model, ok := view.SearchModels(search)
		if !ok {
			model = "<cancelled>"
		}
		selections <- model
	}()
	waitFor(t, view, "Download model", "Model name or owner/repository[:quant]", "Type at least 2 characters", "select", "back")
	view.HandleInput("q")
	waitFor(t, view, "Type at least 2 characters")
	view.HandleInput("w")
	waitFor(t, view, "Searching Hugging Face…")
	text := waitFor(t, view, "owner/qwen-GGUF  1.3M downloads")
	if strings.Contains(text, "other/llama-GGUF") {
		t.Fatalf("fuzzy filter kept a non-matching model:\n%s", text)
	}
	view.HandleInput("\x7f")
	view.HandleInput("w")
	waitFor(t, view, "owner/qwen-GGUF")
	if searches.Load() != 1 {
		t.Fatalf("searches = %d, want 1 (cached query)", searches.Load())
	}
	view.HandleInput(keyEnter)
	if got := result(t, selections); got != "owner/qwen-GGUF" {
		t.Fatalf("selection = %q", got)
	}

	go func() {
		model, _ := view.SearchModels(search)
		selections <- model
	}()
	waitFor(t, view, "Type at least 2 characters")
	for _, key := range "a/b:Q4_K_M" {
		view.HandleInput(string(key))
	}
	view.HandleInput(keyEnter)
	if got := result(t, selections); got != "a/b:Q4_K_M" {
		t.Fatalf("exact selection = %q", got)
	}

	go func() {
		model, ok := view.SearchModels(search)
		if !ok {
			model = "<cancelled>"
		}
		selections <- model
	}()
	waitFor(t, view, "Type at least 2 characters")
	view.HandleInput("e")
	view.HandleInput("r")
	view.HandleInput("r")
	waitFor(t, view, "Hugging Face rate limit reached")
	view.HandleInput(keyEsc)
	if got := result(t, selections); got != "<cancelled>" {
		t.Fatalf("cancelled selection = %q", got)
	}
}

func TestCompactCountAndContextLabels(t *testing.T) {
	for input, want := range map[float64]string{999: "999", 1500: "1.5k", 150_000: "150k", 2_500_000: "2.5M", 25_000_000: "25M"} {
		if got := compactCount(input); got != want {
			t.Errorf("compactCount(%v) = %q, want %q", input, got, want)
		}
	}
	model := LlamaModelInfo{Status: LlamaModelInfoStatus{Value: LlamaModelStatusLoaded, Args: []string{"--ctx-size", "512"}}}
	if got := modelDescription(model); got != "loaded · 512 context" {
		t.Fatalf("description = %q", got)
	}
	if got := modelDescription(LlamaModelInfo{Status: LlamaModelInfoStatus{Value: LlamaModelStatusUnloaded}}); got != "" {
		t.Fatalf("unloaded description = %q", got)
	}
}
