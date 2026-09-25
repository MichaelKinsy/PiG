package codingagent

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// promptObserver is an extension that forwards its ui_prompt_start events.
func promptObserver(name string, seen chan<- string) extension.Extension {
	return extension.Extension{
		Name: name,
		Handlers: map[string][]extension.HandlerFn{
			"ui_prompt_start": {func(args ...any) (any, error) {
				event := args[0].(extension.UIPromptStartEvent)
				seen <- name + ":" + string(event.Kind) + ":" + event.Title
				return nil, nil
			}},
		},
	}
}

// Subprocess dialogs reach the TUI through the bridge, which reports them
// through the runner that is current when the dialog arrives. A reload
// replaces the runner, so it must rebind the bridge.
func TestReplaceExtensionRunnerRebindsSubprocessPromptScope(t *testing.T) {
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&struct{ extension.UIContext }{extension.NoopUIContext})
	m := &InteractiveMode{
		tuiInst: tui.NewWithOutput(io.Discard, 80, 24),
		layout:  tui.NewContainer(),
		opts:    InteractiveOptions{CWD: t.TempDir(), SubprocessUIBridge: bridge},
	}
	seen := make(chan string, 4)
	selectCall := func(title string) {
		args, _ := json.Marshal(map[string]any{"title": title, "options": []string{"a"}})
		if _, err := bridge.HandleCall("ext", &subprocess.CallPayload{Method: "ui.select", Args: args}); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(want string) {
		t.Helper()
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("ui_prompt_start = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("ui_prompt_start %q not delivered", want)
		}
	}

	if errs := m.replaceExtensionRunner([]extension.Extension{promptObserver("first", seen)}); len(errs) != 0 {
		t.Fatal(errs)
	}
	selectCall("Before")
	expect("first:select:Before")

	if errs := m.replaceExtensionRunner([]extension.Extension{promptObserver("second", seen)}); len(errs) != 0 {
		t.Fatal(errs)
	}
	selectCall("After")
	expect("second:select:After")
	select {
	case extra := <-seen:
		t.Fatalf("replaced runner still received %q", extra)
	case <-time.After(50 * time.Millisecond):
	}
}
