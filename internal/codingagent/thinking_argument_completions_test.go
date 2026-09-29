package codingagent

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// thinkingCompletionMode is an editor dispatch mode whose model supports
// thinking levels.
func thinkingCompletionMode(t *testing.T) *InteractiveMode {
	t.Helper()
	m, _ := newCustomEditorDispatchMode(t)
	model, ok := ai.LookupModelExact("amazon-bedrock/anthropic.claude-fable-5")
	if !ok {
		t.Fatal("missing fixture model")
	}
	m.opts.Model = model.ToModel()
	if len(levelsForModel(m.opts.Model)) == 0 {
		t.Fatal("fixture model has no thinking levels")
	}
	return m
}

// Upstream gives /thinking argument completions from the session's available
// thinking levels (interactive-mode.ts:713-726).
func TestThinkingCommandCompletesAvailableLevels(t *testing.T) {
	m := thinkingCompletionMode(t)
	suggestions := m.buildAutocompleteProvider().GetSuggestions([]string{"/thinking h"}, 0, len("/thinking h"))
	if suggestions == nil {
		t.Fatal("no completions after /thinking h")
	}
	var values []string
	for _, item := range suggestions.Items {
		values = append(values, item.Value)
	}
	if !slices.Contains(values, "high") {
		t.Fatalf("completions after /thinking h = %v, want high", values)
	}
}

// Enter on a /thinking argument completion accepts it without submitting, as
// upstream's editor does for a completion prefix that does not start with
// "/" (editor.ts:787-811).
func TestThinkingArgumentCompletionAcceptsWithoutSubmitting(t *testing.T) {
	m := thinkingCompletionMode(t)
	ctx, cancel := context.WithCancel(t.Context())
	input := []string{"/", "t", "h", "i", "n", "k", "i", "n", "g", " ", "h", "i", "g", "h"}
	tasks := make(chan func(), len(input)+1)
	m.editor.SetAsyncApply(func(fn func()) {
		select {
		case tasks <- fn:
		case <-ctx.Done():
		}
	})
	m.editor.SetAutocompleteTaskOwner(ctx, m.backgroundTasks.Go, func(err error) { t.Error(err) })
	t.Cleanup(func() { cancel(); m.backgroundTasks.Wait(); m.abortFn() })
	m.editor.SetAutocomplete(m.buildAutocompleteProvider())
	for _, key := range input {
		if err := m.dispatchKey(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !m.editor.AutocompleteOpen() {
		select {
		case task := <-tasks:
			task()
		case <-deadline.C:
			t.Fatal("the /thinking level completions never arrived")
		}
	}
	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := m.editor.Text(); got != "/thinking high" {
		t.Fatalf("editor after Enter = %q, want the accepted level left in the editor", got)
	}
}
