package codingagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui/widthx"
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
	ctx, cancel := context.WithCancel(t.Context())
	input := []string{"/", "m", "c", "p", " "}
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

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	runQueued := func() {
		for {
			select {
			case task := <-tasks:
				task()
			default:
				return
			}
		}
	}
	// A person types a character after the screen showed the one before: the popup of "/" is open when "m" arrives, and Pi's insertCharacter then refreshes it
	// for every following character, the space included, which opens the command's argument completions. The editor's async results apply on the owner loop,
	// so each key is followed by the tasks it queued.
	for _, key := range input {
		if err := m.dispatchKey(context.Background(), key); err != nil {
			t.Fatal(err)
		}
		runQueued()
		for key == "/" && !m.editor.AutocompleteOpen() {
			select {
			case task := <-tasks:
				task()
			case <-deadline.C:
				t.Fatal("the slash commands' popup never opened")
			}
		}
	}
	// The popup of the commands is open by now; Enter must find the command's argument items in it, not the command names.
	for !strings.Contains(widthx.StripAnsi(strings.Join(m.editor.Render(100), "\n")), "reconnect") {
		select {
		case task := <-tasks:
			task()
		case <-deadline.C:
			t.Fatal("the command's argument completions never arrived")
		}
	}
	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := m.editor.Text(); got != "/mcp reconnect" {
		t.Fatalf("editor after Enter = %q, want the accepted argument left in the editor", got)
	}
}
