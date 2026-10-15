package extensionconformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// editorGetterUI is the recording UI with an autocomplete provider, which the host editor of interactive mode always has: Node's editor host
// installs a component only once the host answers its current provider (editor-component.mjs install), so without one a Node editor never
// reaches the host and another extension cannot replace or restore it.
type editorGetterUI struct{ *recordingUI }

func (editorGetterUI) AutocompleteProvider(context.Context) (*extension.AutocompleteProvider, error) {
	return &extension.AutocompleteProvider{}, nil
}

// editorGetterStep is one step of the getEditorComponent sequence: a fixture command, or a host action, and the state the next probe must report.
type editorGetterStep struct {
	command string
	host    func(t *testing.T, h *harness)
	want    string
}

// restoreHostEditorFromPeer is another extension's setEditorComponent(undefined) reaching the host while the fixture's editor is installed: the
// host restores its own editor and closes the fixture's (ui_bridge.go handleSetEditorComponent).
func restoreHostEditorFromPeer(t *testing.T, h *harness) {
	t.Helper()
	waitInstalledEditor(t, h)
	if _, err := h.bridge.HandleCall("editor-getter-peer", &subprocess.CallPayload{Method: "ui.setEditorComponent", Args: json.RawMessage(`{"clear":true}`)}); err != nil {
		t.Fatalf("peer setEditorComponent(undefined): %v", err)
	}
}

// runEditorGetterSteps runs steps and checks each probe's report, in order.
func runEditorGetterSteps(t *testing.T, h *harness, steps []editorGetterStep) {
	t.Helper()
	ctx := context.Background()
	reportsOf := func() []string {
		var states []string
		for _, n := range h.ui.Recorded() {
			if rest, ok := strings.CutPrefix(n, "editor_get:"); ok {
				states = append(states, strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end"))
			}
		}
		return states
	}
	reports := len(reportsOf())
	for i, step := range steps {
		if step.host != nil {
			step.host(t, h)
		} else if !h.runner.ExecuteCommand(ctx, step.command, "") {
			t.Fatalf("command %s not found", step.command)
		}
		if step.want == "" {
			continue
		}
		reports++
		var got string
		pollUntilConformance(t, step.command+": no report", func() bool {
			states := reportsOf()
			if len(states) < reports {
				return false
			}
			got = states[reports-1]
			return true
		})
		if got != step.want {
			t.Fatalf("after step %d, getEditorComponent reported %q, want %q", i+1, got, step.want)
		}
	}
}

// TestConformance_EditorComponentGetterAgreesAcrossSDKs: Pi's ctx.ui.getEditorComponent() (types.ts:281) returns the configured editor factory
// (interactive-mode.ts:2664 `this.editorComponentFactory`, set first thing by setCustomEditorComponent :2876): the factory the extension installed,
// and undefined before the install, after its own setEditorComponent(undefined), and after another extension's setEditorComponent(undefined) restored
// the default editor. Every SDK reports the same state sequence, isolated and packed. The fixtures report "installed" only for the factory they
// installed (identity in Node, Python and Rust; the function's code in Go), so a getter that answers a different factory fails.
func TestConformance_EditorComponentGetterAgreesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	steps := []editorGetterStep{
		{command: "editor-get-probe", want: "absent"},
		{command: "editor-install"},
		{command: "editor-get-probe", want: "installed"},
		{command: "editor-get-probe", want: "installed"},
		{command: "editor-clear"},
		{command: "editor-get-probe", want: "absent"},
		{command: "editor-install"},
		{command: "editor-get-probe", want: "installed"},
		{command: "another extension restores the host editor", host: restoreHostEditorFromPeer},
		{command: "editor-get-probe", want: "absent"},
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			h.bridge.SetUIContext(editorGetterUI{h.ui})
			runEditorGetterSteps(t, h, steps)
		})
	}
}
