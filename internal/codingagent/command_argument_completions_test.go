package codingagent

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Upstream's editor shows an extension command's getArgumentCompletions
// after "/<command> ", and Enter accepts the highlighted argument without
// submitting (editor.ts tui.select.confirm returns for a prefix that does not
// start with "/"). pi-mcp-adapter's "/mcp " therefore becomes "/mcp reconnect".
func TestExtensionCommandArgumentCompletionAcceptsWithoutSubmitting(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	command := extension.RegisteredCommand{
		Name: "mcp",
		GetArgumentCompletions: func(prefix string) ([]extension.AutocompleteItem, error) {
			return []extension.AutocompleteItem{
				{Value: "reconnect", Label: "reconnect — Reconnect servers"},
				{Value: "tools", Label: "tools — List all tools"},
			}, nil
		},
		Handler: func(context.Context, string) error {
			t.Error("Enter on an argument completion ran the command")
			return nil
		},
	}
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		Name:         "adapter",
		Commands:     map[string]extension.RegisteredCommand{"mcp": command},
		CommandOrder: []string{"mcp"},
	}}, "")
	answered := make(chan struct{}, 4)
	m.commandArgumentCompletions = &commandArgumentCompletions{refresh: func() { answered <- struct{}{} }}
	m.argumentCompletionsOnce.Do(func() {})
	m.editor.SetAutocomplete(m.buildAutocompleteProvider())

	for _, key := range []string{"/", "m", "c", "p", " "} {
		if err := m.dispatchKey(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("the command's argument completions never arrived")
	}
	m.editor.RefreshAutocomplete()
	if !m.editor.AutocompleteOpen() {
		t.Fatal("argument completions did not open the popup")
	}
	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := m.editor.Text(); got != "/mcp reconnect" {
		t.Fatalf("editor after Enter = %q, want the accepted argument left in the editor", got)
	}
}
