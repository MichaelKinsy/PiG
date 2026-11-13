package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestRPCUIDialogWithAnAbortedSignalWritesNoRequest ports rpc-mode.ts:101
// (`if (opts?.signal?.aborted) return Promise.resolve(defaultValue)`): a dialog
// whose signal is already aborted resolves its default before any
// extension_ui_request is written, so the client never sees a dialog it cannot
// answer. Each dialog kind asserts its own default.
func TestRPCUIDialogWithAnAbortedSignalWritesNoRequest(t *testing.T) {
	var written []any
	ui := newRPCUIContext(func(value any) { written = append(written, value) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opts := extension.ExtensionUIDialogOptions{}

	if value, err := ui.Select(ctx, "Pick", []string{"a"}, opts); value != "" || !errors.Is(err, context.Canceled) {
		t.Errorf("select = %q, %v; want the empty default and context.Canceled", value, err)
	}
	if ok, err := ui.Confirm(ctx, "Sure", "?", opts); ok || !errors.Is(err, context.Canceled) {
		t.Errorf("confirm = %v, %v; want false and context.Canceled", ok, err)
	}
	if value, err := ui.Input(ctx, "Name", "x", opts); value != "" || !errors.Is(err, context.Canceled) {
		t.Errorf("input = %q, %v; want the empty default and context.Canceled", value, err)
	}
	if len(written) != 0 {
		t.Fatalf("a dialog with an aborted signal wrote %d request(s): %v", len(written), written)
	}
	if ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"anything","value":"late"}`)) {
		t.Error("an aborted dialog left a pending entry")
	}
}

// TestRPCUIEditorDialogHasNoTimeoutAndResolvesOnTheResponse covers ui.editor
// (rpc-mode.ts:258), which unlike select/confirm/input takes no signal and no
// timeout: its request carries no timeout member and the dialog stays pending
// until the response of the client resolves it.
func TestRPCUIEditorDialogHasNoTimeoutAndResolvesOnTheResponse(t *testing.T) {
	requests := make(chan map[string]any, 1)
	ui := newRPCUIContext(func(value any) {
		data, _ := json.Marshal(value)
		var request map[string]any
		_ = json.Unmarshal(data, &request)
		requests <- request
	})
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := ui.Editor(context.Background(), "Edit", "draft")
		done <- result{value, err}
	}()
	request := <-requests
	if _, ok := request["timeout"]; ok || request["method"] != "editor" {
		t.Fatalf("editor request = %v, want an editor request without a timeout member", request)
	}
	select {
	case got := <-done:
		t.Fatalf("editor resolved before any response: %+v", got)
	default:
	}
	if !ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + request["id"].(string) + `","value":"edited"}`)) {
		t.Fatal("the editor response was not handled")
	}
	if got := <-done; got.err != nil || got.value != "edited" {
		t.Fatalf("editor = %+v, want the edited text", got)
	}
}
