package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts createDialogPromise keeps every open dialog in pendingExtensionRequests, keyed by request id, so
// RPC mode has always let an extension keep several dialogs open at once; interactive mode follows since Pi bdd9f039 (38847563, #11166). This pins the RPC side of
// the cross-mode row: two concurrent dialogs each write their own request, and a response settles the dialog it names whatever the order.
func TestRpcConcurrentExtensionDialogsResolveEachByItsOwnID(t *testing.T) {
	requests := make(chan map[string]any, 2)
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
	first, second := make(chan result, 1), make(chan result, 1)
	go func() {
		value, err := ui.Select(context.Background(), "First", []string{"one", "two"}, extension.ExtensionUIDialogOptions{})
		first <- result{value, err}
	}()
	firstID := (<-requests)["id"].(string)
	go func() {
		value, err := ui.Input(context.Background(), "Second", "", extension.ExtensionUIDialogOptions{})
		second <- result{value, err}
	}()
	secondRequest := <-requests
	if secondRequest["title"] != "Second" {
		t.Fatalf("the second request = %v, want the input titled Second", secondRequest)
	}

	if !ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + secondRequest["id"].(string) + `","value":"typed"}`)) {
		t.Fatal("the response for the later dialog was not handled")
	}
	if got := <-second; got.err != nil || got.value != "typed" {
		t.Fatalf("the later dialog resolved %q, %v; want typed", got.value, got.err)
	}
	select {
	case got := <-first:
		t.Fatalf("the earlier dialog resolved %q, %v when the later one was answered", got.value, got.err)
	default:
	}
	if !ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + firstID + `","value":"two"}`)) {
		t.Fatal("the response for the earlier dialog was not handled")
	}
	if got := <-first; got.err != nil || got.value != "two" {
		t.Fatalf("the earlier dialog resolved %q, %v; want two", got.value, got.err)
	}
}
