package llama

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type heldProgressView struct {
	*LlamaView
	once             sync.Once
	entered, release chan struct{}
}

func (v *heldProgressView) Progress(state ProgressState) <-chan struct{} {
	v.once.Do(func() {
		close(v.entered)
		<-v.release
	})
	return v.LlamaView.Progress(state)
}

// Pi's event loop serializes Object.assign(state, progress), updateProgress(state), and progress(state).
// Updates cannot be discarded between taking the initial state and showing the progress view.
func TestRunWithProgressPublishesUpdatesDuringInitialMount(t *testing.T) {
	view := &heldProgressView{
		LlamaView: NewLlamaView(func() {}),
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	finish := make(chan struct{})
	done := make(chan struct{})
	updating := make(chan struct{})
	updated := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = RunWithProgress(view, RunWithProgressOptions[int]{
			Title: "Loading model", Model: "alpha", InitialMessage: "Starting…",
			Run: func(_ context.Context, update func(LlamaProgress)) (int, error) {
				<-view.entered
				close(updating)
				half := 0.5
				update(LlamaProgress{Message: "Still going", Ratio: &half, Detail: "1 B / 2 B", keys: progressRatioKey | progressDetailKey})
				close(updated)
				<-finish
				return 1, nil
			},
		})
	}()
	defer func() {
		close(finish)
		result(t, done)
	}()
	result(t, updating)
	// Let an unprotected update finish inside the held mount. A serialized update waits for release instead.
	select {
	case <-updated:
	case <-time.After(100 * time.Millisecond):
	}
	close(view.release)
	result(t, updated)
	// The view's mutex joins the mount and update before its state is inspected.
	waitFor(t, view.LlamaView, "Loading model")
	text := plain(view.LlamaView)
	for _, want := range []string{"Still going", "50%", "1 B / 2 B"} {
		if !strings.Contains(text, want) {
			t.Fatalf("initial mount lost progress %q:\n%s", want, text)
		}
	}
}
