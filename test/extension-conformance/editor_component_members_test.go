package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// installHooksEditor installs the fixture's CustomEditor-style component (editor-install-hooks) and binds the stub host to it.
func installHooksEditor(t *testing.T, tc harnessCase) (extension.RemoteEditor, *baseEditorHost) {
	t.Helper()
	h := tc.make(t)
	cleanupHarness(t, h)
	if !h.runner.ExecuteCommand(context.Background(), "editor-install-hooks", "") {
		t.Fatal("fixture has no editor-install-hooks command")
	}
	editor := waitInstalledEditor(t, h)
	host := &baseEditorHost{inputs: make(chan struct{}, 16), paddingX: -1, autocompleteMax: -1}
	editor.Bind(host)
	editor.Configure(extension.RemoteEditorConfig{Focused: true})
	return editor, host
}

func waitHostText(t *testing.T, host *baseEditorHost, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if text, _ := host.snapshot(); text == want {
			return
		}
		if time.Now().After(deadline) {
			text, _ := host.snapshot()
			t.Fatalf("the host's editor text = %q; want %q", text, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func sendEditorKey(t *testing.T, editor extension.RemoteEditor, host *baseEditorHost, key string) {
	t.Helper()
	editor.Input(key)
	select {
	case <-host.inputs:
	case <-time.After(20 * time.Second):
		t.Fatalf("the component never finished the key %q", key)
	}
}

// Pi's CustomEditor.handleInput runs the handler a subclass set (onEscape, onCtrlD, onPasteImage, an onAction entry, onExtensionShortcut)
// in place of the host's. Every SDK's component sets them through its base, which lists them in the handleInput call; the host answers
// the key of one of them with the hook and the component runs its handler inside handleInput. A key without a handler of the component
// goes to the host. (Which key reaches which handler is the host's, TestDelegatedEditorHooksMatchPi in internal/codingagent.)
func TestEditorComponentAppHandlersRunInTheExtension(t *testing.T) {
	t.Parallel()
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			editor, host := installHooksEditor(t, tc)
			for _, step := range []struct{ name, key, want string }{
				{"onEscape", "\x1b", "raw:hook=escape"},
				{"onCtrlD", "\x04", "raw:hook=ctrl-d"},
				{"onPasteImage", "\x16", "raw:hook=paste-image"},
				{"onAction", "\x03", "raw:hook=clear"},
			} {
				editor.SetText("")
				waitHostText(t, host, "")
				sendEditorKey(t, editor, host, step.key)
				waitHostText(t, host, step.want)
			}

			// Ctrl+D hands the key to the component's onCtrlD only on an empty editor: with text the host's editor handles it.
			sendEditorKey(t, editor, host, "\x04")
			waitHostText(t, host, "raw:hook=clear\x04")

			// The component's onExtensionShortcut runs first, and a key it consumes never reaches the host's handleInput.
			host.mu.Lock()
			before := len(host.handleInputs)
			host.mu.Unlock()
			sendEditorKey(t, editor, host, "S")
			waitHostText(t, host, "raw:hook=shortcut")
			host.mu.Lock()
			after := len(host.handleInputs)
			host.mu.Unlock()
			if after != before {
				t.Fatalf("the host's handleInput saw %d keys after a key the component's onExtensionShortcut consumed; want none", after-before)
			}

			// An ordinary key lists every app action the component handles, in the order Pi's handleInput consults them.
			editor.SetText("")
			waitHostText(t, host, "")
			sendEditorKey(t, editor, host, "x")
			waitHostText(t, host, "x")
			host.mu.Lock()
			hooks := host.hooksSeen[len(host.hooksSeen)-1]
			host.mu.Unlock()
			if want := []string{"app.interrupt", "app.exit", "app.clipboard.pasteImage", "app.clear", "extensionShortcut"}; !slices.Equal(hooks, want) {
				t.Fatalf("handleInput listed the hooks %q; want %q", hooks, want)
			}
		})
	}
}

// The members CustomEditor inherits from Pi's Editor, which a subclass reads and sets on itself: getPaddingX, getAutocompleteMaxVisible, borderColor,
// disableSubmit, focused, setAutocompleteProvider, and the onSubmit and onChange callbacks the host bound. Each is an operation on the host's editor,
// whose answers (padding 4, 9 visible rows, a border color that brackets its text) the fixture's "M" key writes back as text.
func TestEditorComponentInheritedMembersReachTheHost(t *testing.T) {
	t.Parallel()
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			editor, host := installHooksEditor(t, tc)
			sendEditorKey(t, editor, host, "M")
			waitHostText(t, host, "raw:members=padding=4,max=9,border=[x],disable=true,focused=true")
			host.mu.Lock()
			defer host.mu.Unlock()
			if !host.disableSubmit || !host.focused {
				t.Errorf("disableSubmit = %v, focused = %v on the host; want both set", host.disableSubmit, host.focused)
			}
			var provider, want any
			if err := json.Unmarshal(host.provider, &provider); err != nil {
				t.Fatalf("setAutocompleteProvider argument %q: %v", host.provider, err)
			}
			_ = json.Unmarshal([]byte(`{"commands":[{"name":"help","description":"Show help"}],"basePath":"/work"}`), &want)
			if !reflect.DeepEqual(provider, want) {
				t.Errorf("setAutocompleteProvider reached the host as %s; want %v", host.provider, want)
			}
			if !slices.Equal(host.submits, []string{"sub"}) || !slices.Equal(host.changes, []string{"chg"}) {
				t.Errorf("onSubmit saw %q and onChange %q; want the component's calls", host.submits, host.changes)
			}
		})
	}
}

// The host tells a component that embeds the working status its indicator (setWorkingStatusIndicator, with each frame and when it clears) and the
// theme change (invalidate), which Pi's TUI delivers to every component. Neither is a call the component makes.
func TestEditorComponentHearsTheWorkingStatusAndInvalidate(t *testing.T) {
	t.Parallel()
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			editor, host := installHooksEditor(t, tc)
			status := extension.RemoteEditorStatus{Kind: "working", Message: "Thinking", Frames: []string{"a", "b"}, Frame: 1}
			editor.Configure(extension.RemoteEditorConfig{Focused: true, WorkingStatus: &status})
			waitHostText(t, host, "raw:status=working/Thinking/1")
			status.Frame = 0
			editor.Configure(extension.RemoteEditorConfig{Focused: true, WorkingStatus: &status})
			waitHostText(t, host, "raw:status=working/Thinking/0")
			editor.Configure(extension.RemoteEditorConfig{Focused: true})
			waitHostText(t, host, "raw:status=none")

			invalidator, ok := editor.(extension.RemoteEditorInvalidator)
			if !ok {
				t.Fatal("the installed editor does not pass the theme change on")
			}
			invalidator.Invalidate()
			waitHostText(t, host, "raw:invalidated")
		})
	}
}
