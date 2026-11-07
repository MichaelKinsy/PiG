package extensionconformance

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The host waits, with no deadline, for ui.editor.inputDone after every key it sends an editor component. A component whose handleInput panics, raises or
// signals is reported as "editor handleInput failed: <message>" in every SDK, the key still finishes, and the keys after it reach the component.
func TestEditorComponentFailingKeyIsReportedAndFinished(t *testing.T) {
	t.Parallel()
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			if !h.runner.ExecuteCommand(context.Background(), "editor-install", "") {
				t.Fatal("fixture has no editor-install command")
			}
			editor := waitInstalledEditor(t, h)
			host := &baseEditorHost{inputs: make(chan struct{}, 16)}
			editor.Bind(host)
			editor.Configure(extension.RemoteEditorConfig{Focused: true})
			waitInput := func(what string) {
				t.Helper()
				select {
				case <-host.inputs:
				case <-time.After(20 * time.Second):
					t.Fatalf("%s: the key never finished (no ui.editor.inputDone)", what)
				}
			}
			editor.Input("!")
			waitInput("the failing key")
			waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "editor handleInput failed: bad key:error") })
			editor.Input("b")
			waitInput("the key after it")
			if text, _ := host.snapshot(); text != "b" {
				t.Fatalf("super text = %q, want %q: the key after the failing one did not reach the component", text, "b")
			}
		})
	}
}
