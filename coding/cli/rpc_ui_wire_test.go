package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// rpcWireLine is the line the RPC writer produces for request, with the request's random id replaced by ID.
func rpcWireLine(t *testing.T, request any) string {
	t.Helper()
	line, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var id struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(line, &id); err != nil || id.ID == "" {
		t.Fatalf("request has no id: %s (%v)", line, err)
	}
	return strings.Replace(string(line), `"id":"`+id.ID+`"`, `"id":"ID"`, 1)
}

// captureRPCWire runs call against a fresh rpcUIContext and returns the one request it writes. A dialog stays pending after it writes its request, so
// the call runs in the background under a context this cancels once the request is out.
func captureRPCWire(t *testing.T, call func(ctx context.Context, ui *rpcUIContext)) string {
	t.Helper()
	written := make(chan string, 4)
	ui := newRPCUIContext(func(value any) { written <- rpcWireLine(t, value) })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(ctx, ui)
	}()
	line := <-written
	cancel()
	<-done
	select {
	case extra := <-written:
		t.Fatalf("one call wrote a second request: %s after %s", extra, line)
	default:
	}
	return line
}

// Pi writes `{ type, id, method, ...members }` through JSON.stringify (rpc-mode.ts createExtensionUIContext, output), which keeps "", [] and 0 and drops only `undefined`.
// Each golden is that stringification of the literal object the cited Pi method builds, in its key order.
func TestRPCUIWireKeepsEmptyMembersAndOmitsUndefinedOnes(t *testing.T) {
	timeout := func(ms float64) extension.ExtensionUIDialogOptions {
		return extension.ExtensionUIDialogOptions{Timeout: &ms}
	}
	none := extension.ExtensionUIDialogOptions{}
	prefix := `{"type":"extension_ui_request","id":"ID","method":`
	cases := []struct {
		name string
		call func(ctx context.Context, ui *rpcUIContext)
		want string
	}{
		// notify: { method: "notify", message, notifyType: type }
		{"notify empty message, no type", func(_ context.Context, ui *rpcUIContext) { ui.Notify("", "") }, prefix + `"notify","message":""}`},
		{"notify with type", func(_ context.Context, ui *rpcUIContext) { ui.Notify("m", "warning") }, prefix + `"notify","message":"m","notifyType":"warning"}`},
		{"notify empty message with type", func(_ context.Context, ui *rpcUIContext) { ui.Notify("", "error") }, prefix + `"notify","message":"","notifyType":"error"}`},
		// setStatus: { method: "setStatus", statusKey: key, statusText: text }
		{"setStatus empty text", func(_ context.Context, ui *rpcUIContext) { ui.SetStatus("k", "") }, prefix + `"setStatus","statusKey":"k","statusText":""}`},
		{"setStatus text", func(_ context.Context, ui *rpcUIContext) { ui.SetStatus("k", "t") }, prefix + `"setStatus","statusKey":"k","statusText":"t"}`},
		{"setStatus undefined text", func(_ context.Context, ui *rpcUIContext) { ui.ClearStatus("k") }, prefix + `"setStatus","statusKey":"k"}`},
		{"setStatus empty key", func(_ context.Context, ui *rpcUIContext) { ui.SetStatus("", "t") }, prefix + `"setStatus","statusKey":"","statusText":"t"}`},
		// setWidget: { method: "setWidget", widgetKey: key, widgetLines: content, widgetPlacement: options?.placement }
		{"setWidget empty lines", func(_ context.Context, ui *rpcUIContext) { ui.SetWidget("w", []string{}, nil) }, prefix + `"setWidget","widgetKey":"w","widgetLines":[]}`},
		{"setWidget undefined lines", func(_ context.Context, ui *rpcUIContext) { ui.SetWidget("w", nil, nil) }, prefix + `"setWidget","widgetKey":"w"}`},
		{"setWidget empty key", func(_ context.Context, ui *rpcUIContext) { ui.SetWidget("", []string{"a"}, nil) }, prefix + `"setWidget","widgetKey":"","widgetLines":["a"]}`},
		{"setWidget empty line", func(_ context.Context, ui *rpcUIContext) { ui.SetWidget("w", []string{""}, nil) }, prefix + `"setWidget","widgetKey":"w","widgetLines":[""]}`},
		// setTitle: { method: "setTitle", title }
		{"setTitle empty", func(_ context.Context, ui *rpcUIContext) { ui.SetTitle("") }, prefix + `"setTitle","title":""}`},
		// setEditorText: { method: "set_editor_text", text }; pasteToEditor calls it
		{"set_editor_text empty", func(_ context.Context, ui *rpcUIContext) { ui.SetEditorText("") }, prefix + `"set_editor_text","text":""}`},
		{"pasteToEditor empty", func(_ context.Context, ui *rpcUIContext) { ui.PasteToEditor("") }, prefix + `"set_editor_text","text":""}`},
		// select: { method: "select", title, options, timeout: opts?.timeout }
		{"select empty title and options", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Select(ctx, "", []string{}, none) }, prefix + `"select","title":"","options":[]}`},
		{"select empty option", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Select(ctx, "T", []string{""}, none) }, prefix + `"select","title":"T","options":[""]}`},
		{"select undefined options", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Select(ctx, "T", nil, none) }, prefix + `"select","title":"T"}`},
		{"select timeout 0", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Select(ctx, "T", []string{}, timeout(0)) }, prefix + `"select","title":"T","options":[],"timeout":0}`},
		{"select timeout 50", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Select(ctx, "T", []string{}, timeout(50)) }, prefix + `"select","title":"T","options":[],"timeout":50}`},
		// confirm: { method: "confirm", title, message, timeout: opts?.timeout }
		{"confirm empty title and message", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Confirm(ctx, "", "", none) }, prefix + `"confirm","title":"","message":""}`},
		{"confirm timeout 0", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Confirm(ctx, "", "", timeout(0)) }, prefix + `"confirm","title":"","message":"","timeout":0}`},
		{"confirm timeout 50", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Confirm(ctx, "", "", timeout(50)) }, prefix + `"confirm","title":"","message":"","timeout":50}`},
		// input: { method: "input", title, placeholder, timeout: opts?.timeout }
		{"input empty placeholder", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Input(ctx, "T", "", none) }, prefix + `"input","title":"T","placeholder":""}`},
		{"input placeholder", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Input(ctx, "", "p", none) }, prefix + `"input","title":"","placeholder":"p"}`},
		{"input undefined placeholder", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.InputWithoutPlaceholder(ctx, "T", none) }, prefix + `"input","title":"T"}`},
		{"input undefined placeholder, timeout 0", func(ctx context.Context, ui *rpcUIContext) {
			_, _ = ui.InputWithoutPlaceholder(ctx, "T", timeout(0))
		}, prefix + `"input","title":"T","timeout":0}`},
		{"input empty placeholder, timeout 50", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Input(ctx, "T", "", timeout(50)) }, prefix + `"input","title":"T","placeholder":"","timeout":50}`},
		// editor: { method: "editor", title, prefill }
		{"editor empty prefill", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Editor(ctx, "E", "") }, prefix + `"editor","title":"E","prefill":""}`},
		{"editor prefill", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.Editor(ctx, "", "x") }, prefix + `"editor","title":"","prefill":"x"}`},
		{"editor undefined prefill", func(ctx context.Context, ui *rpcUIContext) { _, _ = ui.EditorWithoutPrefill(ctx, "E") }, prefix + `"editor","title":"E"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := captureRPCWire(t, tc.call); got != tc.want {
				t.Fatalf("wire = %s\nwant   %s", got, tc.want)
			}
		})
	}
}

// Every SDK's host call reaches the RPC writer through the UI bridge, which decodes the call's arguments. An absent member is Pi's undefined and an empty one a value,
// all the way to the wire: a Node extension's `ui.setStatus(k, undefined)` sends no `text`, its `ui.input("T")` no `placeholder`, and the Go, Python and Rust SDKs
// send no placeholder/prefill for an empty one (extensions/sdk withNonEmpty).
func TestRPCUIBridgeCallsKeepEmptyMembersAndOmitUnsetOnes(t *testing.T) {
	prefix := `{"type":"extension_ui_request","id":"ID","method":`
	cases := []struct {
		name   string
		method string
		args   string
		want   string
	}{
		{"setStatus empty text", "ui.setStatus", `{"key":"k","text":""}`, prefix + `"setStatus","statusKey":"k","statusText":""}`},
		{"setStatus no text", "ui.setStatus", `{"key":"k"}`, prefix + `"setStatus","statusKey":"k"}`},
		{"setStatus null text", "ui.setStatus", `{"key":"k","text":null}`, prefix + `"setStatus","statusKey":"k"}`},
		{"notify empty message, no level", "ui.notify", `{"message":""}`, prefix + `"notify","message":""}`},
		{"notify empty message, empty level", "ui.notify", `{"message":"","level":""}`, prefix + `"notify","message":""}`},
		{"setTitle empty", "ui.setTitle", `{"title":""}`, prefix + `"setTitle","title":""}`},
		{"setEditorText empty", "ui.setEditorText", `{"text":""}`, prefix + `"set_editor_text","text":""}`},
		{"pasteToEditor empty", "ui.pasteToEditor", `{"text":""}`, prefix + `"set_editor_text","text":""}`},
		{"select empty options", "ui.select", `{"title":"T","options":[],"opts":{}}`, prefix + `"select","title":"T","options":[]}`},
		{"select null options", "ui.select", `{"title":"T","options":null,"opts":{}}`, prefix + `"select","title":"T"}`},
		{"select timeout 0", "ui.select", `{"title":"T","options":[],"opts":{"timeout":0}}`, prefix + `"select","title":"T","options":[],"timeout":0}`},
		{"confirm empty", "ui.confirm", `{"title":"","message":"","opts":{"timeout":50}}`, prefix + `"confirm","title":"","message":"","timeout":50}`},
		{"input no placeholder", "ui.input", `{"title":"T","opts":{}}`, prefix + `"input","title":"T"}`},
		{"input empty placeholder", "ui.input", `{"title":"T","placeholder":"","opts":{}}`, prefix + `"input","title":"T","placeholder":""}`},
		{"editor no prefill", "ui.editor", `{"title":"E"}`, prefix + `"editor","title":"E"}`},
		{"editor empty prefill", "ui.editor", `{"title":"E","prefill":""}`, prefix + `"editor","title":"E","prefill":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			written := make(chan any, 4)
			ui := newRPCUIContext(func(value any) { written <- value })
			bridge := subprocess.NewUIBridge(nil)
			bridge.SetUIContext(ui)
			done := make(chan error, 1)
			go func() {
				_, err := bridge.HandleCall("ext", &subprocess.CallPayload{Method: tc.method, Args: json.RawMessage(tc.args)})
				done <- err
			}()
			request := <-written
			line := rpcWireLine(t, request)
			if line != tc.want {
				t.Fatalf("wire = %s\nwant   %s", line, tc.want)
			}
			// A dialog waits for its response; answer it as cancelled so the call returns.
			if tc.method == "ui.select" || tc.method == "ui.confirm" || tc.method == "ui.input" || tc.method == "ui.editor" {
				var id struct {
					ID string `json:"id"`
				}
				data, _ := json.Marshal(request)
				_ = json.Unmarshal(data, &id)
				if !ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + id.ID + `","cancelled":true}`)) {
					t.Fatal("the dialog was not pending")
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
