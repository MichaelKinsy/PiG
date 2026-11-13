package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// resetRecordingUI records the statuses, the footer and the header a bridge shows a UI.
type resetRecordingUI struct {
	extension.UIContext
	statuses       map[string]string
	footer, header bool
}

func newResetRecordingUI() *resetRecordingUI {
	return &resetRecordingUI{UIContext: extension.NoopUIContext, statuses: map[string]string{}}
}

func (u *resetRecordingUI) SetStatus(key, text string) {
	if text == "" {
		delete(u.statuses, key)
		return
	}
	u.statuses[key] = text
}
func (u *resetRecordingUI) SetFooter(factory extension.FooterFactory) { u.footer = factory != nil }
func (u *resetRecordingUI) SetHeader(factory extension.HeaderFactory) { u.header = factory != nil }

// TestResetExtensionUIForgetsWhatTheBridgeShowsALaterUI: the bridge keeps the statuses, the footer and the header the extension
// processes set, and shows them to a UI bound later (a TUI mode switch binds one). Pi's resetExtensionUI clears them when a /reload
// begins (interactive-mode.ts: setExtensionFooter(undefined), setExtensionHeader(undefined), clearExtensionStatuses), and the reloaded
// extensions set their own: after the reset a UI bound later is shown none of them, and a frame the replaced footer factory renders
// late is not installed.
func TestResetExtensionUIForgetsWhatTheBridgeShowsALaterUI(t *testing.T) {
	bridge := NewUIBridge(func() {})
	first := newResetRecordingUI()
	bridge.SetUIContext(first)
	call := func(method, args string) {
		t.Helper()
		result, err := bridge.HandleCall("ext", &CallPayload{Method: method, Args: json.RawMessage(args)})
		if err != nil || (result != nil && result.Error != nil) {
			t.Fatalf("%s: %v, %+v", method, err, result)
		}
	}
	call("ui.setStatus", `{"key":"k","text":"set before the reload"}`)
	call("ui.setFooter", `{"lines":["footer"],"width":40,"surfaceId":1}`)
	call("ui.setHeader", `{"lines":["header"],"width":40,"surfaceId":1}`)
	if first.statuses["k"] == "" || !first.footer || !first.header {
		t.Fatalf("the UI was shown statuses %v, footer %t, header %t; want each", first.statuses, first.footer, first.header)
	}

	bridge.ResetExtensionUI()
	second := newResetRecordingUI()
	bridge.SetUIContext(second)
	if len(second.statuses) != 0 || second.footer || second.header {
		t.Errorf("a UI bound after the reset was shown statuses %v, footer %t, header %t of the replaced extension processes, want none", second.statuses, second.footer, second.header)
	}
	call("ui.setFooter", `{"lines":["late frame"],"width":40,"surfaceId":1,"updateOnly":true}`)
	call("ui.setHeader", `{"lines":["late frame"],"width":40,"surfaceId":1,"updateOnly":true}`)
	if second.footer || second.header {
		t.Errorf("a frame the replaced factory rendered after the reset was installed: footer %t, header %t", second.footer, second.header)
	}
	// What an extension sets after the reset is its own.
	call("ui.setStatus", `{"key":"k","text":"set after the reload"}`)
	call("ui.setFooter", `{"lines":["footer"],"width":40,"surfaceId":2}`)
	if second.statuses["k"] != "set after the reload" || !second.footer {
		t.Errorf("after the reset the UI was shown statuses %v, footer %t; want what the reloaded extension set", second.statuses, second.footer)
	}
}
