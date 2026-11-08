package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// ceHookProbes are the keys of Pi's CustomEditor.handleInput that reach a handler the component set (the interrupt, exit, paste-image
// and clear actions and an extension shortcut), with each handler set or not, over an empty and a non-empty editor.
func ceHookProbes() []ceOracleProbe {
	var probes []ceOracleProbe
	for _, onEscape := range []bool{false, true} {
		for _, onCtrlD := range []bool{false, true} {
			for _, onPasteImage := range []bool{false, true} {
				for _, actions := range [][]string{{}, {"app.clear"}, {"app.interrupt", "app.exit", "app.clear"}} {
					for _, shortcut := range []string{"", "\x1b"} {
						for _, text := range []string{"", "x"} {
							for _, key := range []string{"\x1b", "\x04", "\x16", "\x03", "a"} {
								probe := ceOracleProbe{Actions: actions, OnEscape: onEscape, OnCtrlD: onCtrlD, OnPasteImage: onPasteImage, Shortcut: shortcut}
								if text != "" {
									probe.Ops = append(probe.Ops, ceOracleOp{Insert: &text, Width: 40})
								}
								probe.Ops = append(probe.Ops, ceOracleOp{Keys: &key, Width: 40})
								probes = append(probes, probe)
							}
						}
					}
				}
			}
		}
	}
	return probes
}

// ceHookCalls are the handlers a component whose handlers the probe describes runs for key, through the hooks protocol: its own
// onExtensionShortcut first, then the app action the host answers as a hook, which the component runs as onEscape, onCtrlD, onPasteImage
// or the actionHandlers entry.
func ceHookCalls(h *remoteEditor, probe ceOracleProbe, key string) []string {
	calls := []string{}
	if probe.Shortcut != "" {
		calls = append(calls, "shortcut")
		if key == probe.Shortcut {
			return calls
		}
	}
	has := func(action string) bool { return slices.Contains(probe.Actions, action) }
	var hooks []string
	if probe.OnEscape || has("app.interrupt") {
		hooks = append(hooks, "app.interrupt")
	}
	if probe.OnCtrlD || has("app.exit") {
		hooks = append(hooks, "app.exit")
	}
	if probe.OnPasteImage {
		hooks = append(hooks, "app.clipboard.pasteImage")
	}
	for _, action := range probe.Actions {
		if !slices.Contains([]string{"app.interrupt", "app.exit", "app.clipboard.pasteImage"}, action) {
			hooks = append(hooks, action)
		}
	}
	id, ok := h.editorHook(classifyKeyWithBindings(key, h.m.keybindings), hooks)
	if !ok {
		return calls
	}
	switch {
	case id == "app.interrupt" && probe.OnEscape:
		return append(calls, "onEscape")
	case id == "app.exit" && probe.OnCtrlD:
		return append(calls, "onCtrlD")
	case id == "app.clipboard.pasteImage":
		return append(calls, "onPasteImage")
	}
	return append(calls, "action:"+id)
}

// A delegated component that sets onEscape, onCtrlD, onPasteImage, actionHandlers entries and onExtensionShortcut runs the same handler
// for each key as Pi's CustomEditor does, through the hooks the host answers: pinned Pi's own CustomEditor is the reference over the
// handlers set or not, the key, an empty or non-empty editor and an extension shortcut that consumes the key or not. (The autocomplete
// popup deciding whether escape reaches the app is TestCustomEditorMatchesPi's.)
func TestDelegatedEditorHooksMatchPi(t *testing.T) {
	probes := ceHookProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/custom_editor.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]ceOracleFrame
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}

	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	fake := &fakeDelegatedEditor{}
	m.setRemoteEditor(fake)
	h := m.remoteEditor
	failures := 0
	for i, probe := range probes {
		var want []string
		for k, op := range probe.Ops {
			if op.Insert != nil {
				m.editor.AsBase(func() { m.editor.SetText(*op.Insert) })
				continue
			}
			want = expected[i][k].Calls
			if want == nil {
				want = []string{}
			}
			got := ceHookCalls(h, probe, *op.Keys)
			if !slices.Equal(got, want) {
				if failures++; failures <= 8 {
					setup, _ := json.Marshal(map[string]any{"actions": probe.Actions, "onEscape": probe.OnEscape, "onCtrlD": probe.OnCtrlD, "onPasteImage": probe.OnPasteImage, "shortcut": probe.Shortcut})
					t.Errorf("probe %d %s: key %q over text %q: hooks ran %q, Pi ran %q", i, setup, *op.Keys, m.editor.Text(), got, want)
				}
			}
		}
		m.editor.AsBase(func() { m.editor.SetText("") })
	}
	if failures > 8 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
