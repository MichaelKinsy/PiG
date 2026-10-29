package codingagent

import (
	"context"
	"testing"
)

// completionReadMode wires the mode's editor as interactive.go does (native completions on owned workers, results posted to the
// owner loop) and records what Enter submits. The posted tasks pile up in tasks: the owner loop reads the next terminal read
// before it runs them, as priorityInput prefers an arrived keystroke to a posted task.
func completionReadMode(t *testing.T, m *InteractiveMode) (submitted *[]string, tasks chan func(), ctx context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	tasks = make(chan func(), 64)
	m.isIdle = true
	m.editor.SetAsyncApply(func(fn func()) {
		select {
		case tasks <- fn:
		case <-ctx.Done():
		}
	})
	m.editor.SetAutocompleteTaskOwner(ctx, m.backgroundTasks.Go, func(err error) { t.Error(err) })
	t.Cleanup(func() { cancel(); m.backgroundTasks.Wait(); m.abortFn() })
	m.editor.SetAutocomplete(m.buildAutocompleteProvider())
	submitted = new([]string)
	m.editor.OnSubmit = func(text string) { *submitted = append(*submitted, text) }
	return submitted, tasks, ctx
}

// dispatchRead handles keys as one terminal read: StdinBuffer emits every sequence of a read before anything else runs, so the
// loop dispatches them back to back and the read ends after the last one (dispatchTerminalRead).
func dispatchRead(t *testing.T, ctx context.Context, m *InteractiveMode, keys ...string) {
	t.Helper()
	rest := make(chan inputChunk, len(keys))
	for i, key := range keys[1:] {
		rest <- inputChunk{data: []byte(key), more: i < len(keys)-2}
	}
	if err := m.dispatchTerminalRead(ctx, inputChunk{data: []byte(keys[0]), more: len(keys) > 1}, &rest, make(chan error)); err != nil {
		t.Fatal(err)
	}
}

func splitKeys(text string) []string {
	var keys []string
	for _, r := range text {
		keys = append(keys, string(r))
	}
	return keys
}

// Pi's editor requests autocomplete from the key handlers and resolves the request in the Promise jobs that follow the
// terminal read (editor.ts startAutocompleteRequest; the provider answers a command name or a command's arguments without
// I/O), so the menu is open when the next read, here an Enter sent right behind the text, arrives. That Enter accepts the
// suggestion: an argument stays in the editor for a second Enter, a command name is accepted and submitted. PiG started the
// request on a worker and published it through two owner-loop tasks that the next read overtook, so its Enter found no
// menu and submitted what was typed: `/model provider/id` typed and entered at once switched the model where Pi only
// completed it.
func TestEnterInTheReadAfterTheTextAcceptsTheMenuThatReadOpened(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mode          func(*testing.T) *InteractiveMode
		typed         string
		wantEditor    string
		wantSubmitted []string
	}{
		{"model argument", func(t *testing.T) *InteractiveMode {
			m, _, _ := liveInputModelMode(t)
			useCaptureModels(t, m)
			return m
		}, "/model capture/model-two", "/model capture/model-two", nil},
		{"thinking argument", thinkingCompletionMode, "/thinking hi", "/thinking high", nil},
		{"command name", func(t *testing.T) *InteractiveMode {
			m, _, _ := liveInputModelMode(t)
			return m
		}, "/settin", "", []string{"/settings"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.mode(t)
			submitted, _, ctx := completionReadMode(t, m)
			dispatchRead(t, ctx, m, splitKeys(tc.typed)...)
			if !m.editor.AutocompleteOpen() {
				t.Fatalf("no autocomplete menu after the read that typed %q: Pi opens it before the next read", tc.typed)
			}
			dispatchRead(t, ctx, m, "\r")
			if got := m.editor.Text(); got != tc.wantEditor {
				t.Fatalf("editor after Enter = %q, want %q", got, tc.wantEditor)
			}
			if len(*submitted) != len(tc.wantSubmitted) || (len(tc.wantSubmitted) > 0 && (*submitted)[0] != tc.wantSubmitted[0]) {
				t.Fatalf("Enter submitted %q, want %q", *submitted, tc.wantSubmitted)
			}
		})
	}
}

// The menu belongs to the end of a read, not to each of its keys: Pi handles all the sequences of one read before any Promise
// job runs, so an Enter in the same read as the text sees no menu and submits what was typed.
func TestEnterInTheSameReadAsTheTextSubmitsIt(t *testing.T) {
	m := thinkingCompletionMode(t)
	submitted, _, ctx := completionReadMode(t, m)
	dispatchRead(t, ctx, m, append(splitKeys("/thinking hi"), "\r")...)
	if len(*submitted) != 1 || (*submitted)[0] != "/thinking hi" {
		t.Fatalf("Enter in the read that typed the text submitted %q, want /thinking hi", *submitted)
	}
	if m.editor.AutocompleteOpen() {
		t.Fatal("the menu of the submitted text is still open")
	}
}
