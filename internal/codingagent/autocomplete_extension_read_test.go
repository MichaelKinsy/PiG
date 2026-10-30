package codingagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// extensionCompletionMode wires the mode's editor as interactive.go does and installs an extension wrapper over the model-argument
// completions. Posted owner tasks run only when the test pumps them, so the next read overtakes them as priorityInput lets it.
func extensionCompletionMode(t *testing.T, wrapper extension.AutocompleteProviderFactory) (m *InteractiveMode, submitted *[]string, ctx context.Context) {
	t.Helper()
	m, _, ctx = liveInputModelMode(t)
	useCaptureModels(t, m)
	m.backgroundCtx = ctx
	m.editor.SetAsyncApply(func(fn func()) { _ = m.postToMain(ctx, fn) })
	m.editor.SetAutocompleteTaskOwner(ctx, m.backgroundTasks.Go, func(err error) { t.Error(err) })
	t.Cleanup(func() { m.backgroundTasks.Wait(); m.abortFn() })
	m.editor.SetAutocomplete(m.buildAutocompleteProvider())
	submitted = new([]string)
	m.editor.OnSubmit = func(text string) { *submitted = append(*submitted, text) }
	autocompleteOwnerCall(t, m, func() error { return (&ExtUIContext{m: m}).AddAutocompleteProvider(wrapper) })
	return m, submitted, ctx
}

func pumpOwnerUntil(t *testing.T, m *InteractiveMode, what string, done func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !done() {
		select {
		case fn := <-m.uiTaskCh:
			fn()
		case <-deadline:
			t.Fatalf("the owner loop never saw %s", what)
		}
	}
}

func passThroughWrapper(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
	return &extension.AutocompleteProvider{GetSuggestions: func(ctx context.Context, lines []string, line, col int, force bool) (*extension.AutocompleteSuggestions, error) {
		return current.GetSuggestions(ctx, lines, line, col, force)
	}, ApplyCompletion: current.ApplyCompletion, ShouldTriggerFileCompletion: current.ShouldTriggerFileCompletion}, nil
}

// An extension's provider that answers without I/O settles in the Promise jobs after the read that typed the text, as the built-in one does
// (editor.ts startAutocompleteRequest awaits the whole wrapper chain there), so the Enter of the next read finds the menu open and
// accepts it. PiG's chain is a round trip through workers and the owner loop, which the next read overtakes unless the host holds it.
func TestEnterInTheReadAfterTheTextAcceptsTheMenuAnExtensionProviderOpened(t *testing.T) {
	m, submitted, ctx := extensionCompletionMode(t, passThroughWrapper)
	const text = "/model capture/model-two"
	dispatchRead(t, ctx, m, splitKeys(text)...)
	dispatchRead(t, ctx, m, "\r")
	seenOpen := false
	pumpOwnerUntil(t, m, "the menu open and the Enter accept it", func() bool {
		seenOpen = seenOpen || m.editor.AutocompleteOpen()
		return len(*submitted) > 0 || (seenOpen && !m.editor.AutocompleteOpen())
	})
	drainAutocompleteWork(t, m)
	if len(*submitted) != 0 {
		t.Fatalf("the first Enter submitted %q: Pi opened the menu before that read and accepted its suggestion", *submitted)
	}
	if got := m.editor.Text(); got != text {
		t.Fatalf("editor after the first Enter = %q, want %q", got, text)
	}
}

// A chain that awaits real I/O has not settled when the next read arrives, so Pi's Enter submits the typed text at once and the late
// answer finds the text gone. The host must not hold the read for such a chain.
func TestEnterInTheReadAfterTheTextSubmitsWhileAnExtensionProviderAwaitsIO(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	m, submitted, ctx := extensionCompletionMode(t, func(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
		return &extension.AutocompleteProvider{GetSuggestions: func(ctx context.Context, _ []string, _, _ int, _ bool) (*extension.AutocompleteSuggestions, error) {
			tui.AutocompleteAwaitsIO(ctx)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil, ctx.Err()
		}, ApplyCompletion: current.ApplyCompletion}, nil
	})
	const text = "/model capture/model-two"
	dispatchRead(t, ctx, m, splitKeys(text)...)
	dispatchRead(t, ctx, m, "\r")
	pumpOwnerUntil(t, m, "the Enter submit", func() bool { return len(*submitted) > 0 })
	if (*submitted)[0] != text {
		t.Fatalf("Enter submitted %q, want %q", *submitted, text)
	}
}

// A synchronous provider that blocks has not settled when the next read arrives, and Pi's own loop is blocked with it: the Enter of the next
// read waits for the provider to return, then accepts the menu it opened.
func TestEnterWaitsForAnExtensionProviderThatBlocksUntilItReturns(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	m, submitted, ctx := extensionCompletionMode(t, func(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
		return &extension.AutocompleteProvider{GetSuggestions: func(ctx context.Context, lines []string, line, col int, force bool) (*extension.AutocompleteSuggestions, error) {
			<-release
			return current.GetSuggestions(ctx, lines, line, col, force)
		}, ApplyCompletion: current.ApplyCompletion}, nil
	})
	const text = "/model capture/model-two"
	dispatchRead(t, ctx, m, splitKeys(text)...)
	dispatchRead(t, ctx, m, "\r")
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case fn := <-m.uiTaskCh:
			fn()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if len(*submitted) != 0 || m.editor.Text() != text {
		t.Fatalf("the Enter was handled while the provider still blocked: submitted %q, editor %q", *submitted, m.editor.Text())
	}
	unblock()
	seenOpen := false
	pumpOwnerUntil(t, m, "the menu open and the Enter accept it", func() bool {
		seenOpen = seenOpen || m.editor.AutocompleteOpen()
		return len(*submitted) > 0 || (seenOpen && !m.editor.AutocompleteOpen())
	})
	drainAutocompleteWork(t, m)
	if len(*submitted) != 0 || m.editor.Text() != text {
		t.Fatalf("after the provider returned: submitted %q, editor %q, want the suggestion accepted", *submitted, m.editor.Text())
	}
}

// The base provider's awaited work (an fd search, a Promise-returning argument completer) is real I/O: the wrapper chain that waits on it
// has left Pi's microtask checkpoint, so the base announces it on the request's context.
type awaitedFileProvider struct{ tui.AutocompleteProvider }

func (awaitedFileProvider) FileSearchTask([]string, int, int) (string, func(context.Context) []tui.AutocompleteItem, bool) {
	return "f", func(context.Context) []tui.AutocompleteItem { return []tui.AutocompleteItem{{Value: "file"}} }, true
}

func TestBaseProviderAnnouncesItsAwaitedWork(t *testing.T) {
	m := autocompleteModeProbe(t)
	for _, tc := range []struct {
		name  string
		base  tui.AutocompleteProvider
		await int
	}{
		{"file search", awaitedFileProvider{tui.NewCombinedProvider(nil, t.TempDir(), "")}, 1},
		{"command names", tui.NewCombinedProvider([]tui.SlashCommand{{Name: "keep"}}, t.TempDir(), ""), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			ctx := tui.WithAutocompleteUnhold(t.Context(), func() { calls++ })
			base := m.baseAutocompleteProvider(tc.base)
			autocompleteOwnerCall(t, m, func() error {
				_, err := base.GetSuggestions(ctx, []string{"/k"}, 0, 2, false)
				return err
			})
			if calls != tc.await {
				t.Fatalf("announced %d times, want %d", calls, tc.await)
			}
		})
	}
}
