package codingagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// A subprocess extension's concurrent ui.select and ui.confirm calls (a Promise.all of ctx.ui calls, or one thread per dialog) reach the
// interactive UI through the host's UI bridge. The bridge neither drops nor queues the second call behind the first: both dialogs sit in
// the editor slot stack, the later one is visible, and each call answers with its own result as its dialog closes
// (interactive-mode.ts pushExtensionDialog, #11166).
func TestExtensionDialogsFromOneExtensionStackThroughTheHostBridge(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: m})
	type outcome struct {
		result *subprocess.CallResultPayload
		err    error
	}
	call := func(method, args string) <-chan outcome {
		out := make(chan outcome, 1)
		go func() {
			result, err := bridge.HandleCall("ext", &subprocess.CallPayload{Method: method, Args: json.RawMessage(args)})
			out <- outcome{result, err}
		}()
		pumpUITask(t, m, method+" did not post its dialog to the UI loop")
		return out
	}
	first := call("ui.select", `{"title":"First","options":["one","two"]}`)
	second := call("ui.confirm", `{"title":"Second","message":"sure?"}`)
	if n := openExtensionDialogs(m); n != 2 {
		t.Fatalf("%d dialogs are open, want both", n)
	}
	requireVisible(t, m, "Second")

	if err := m.dispatchKey(context.Background(), "\x1b"); err != nil {
		t.Fatal(err)
	}
	got := <-second
	var confirmed struct {
		Confirmed bool `json:"confirmed"`
	}
	if got.err != nil || got.result == nil || got.result.Error != nil || json.Unmarshal(got.result.Result, &confirmed) != nil || confirmed.Confirmed {
		t.Fatalf("later confirm answered %+v, %v; want it declined", got.result, got.err)
	}
	select {
	case got := <-first:
		t.Fatalf("earlier select answered %+v, %v when the later dialog closed", got.result, got.err)
	default:
	}
	requireVisible(t, m, "First")

	for _, key := range []string{"\x1b[B", "\r"} {
		if err := m.dispatchKey(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	got = <-first
	var selected struct {
		Selected string `json:"selected"`
		Ok       bool   `json:"ok"`
	}
	if got.err != nil || got.result == nil || got.result.Error != nil || json.Unmarshal(got.result.Result, &selected) != nil || selected.Selected != "two" || !selected.Ok {
		t.Fatalf("earlier select answered %+v, %v; want two", got.result, got.err)
	}
	if n := openExtensionDialogs(m); n != 0 {
		t.Fatalf("%d dialogs are left open", n)
	}
}
