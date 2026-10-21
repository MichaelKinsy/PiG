package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/modes/rpc/rpc-types.ts:293-297 RpcExtensionUIResponse is the union
// `{id, value}` | `{id, confirmed}` | `{id, cancelled: true}`, each with type "extension_ui_response". The reader in rpc-mode.ts:138-150, :254-266
// takes `"cancelled" in r && r.cancelled ? undefined : "value" in r ? r.value : undefined`; an empty-string value is a real answer.
// rpcUIResponse must decode each wire shape so that a present-but-empty value, a false `confirmed` and a missing member stay distinct.
// mutation-checked: Value not decoded, dialogValue reading a nil Value as an empty answer, and Cancelled not decoded each fail it.
func TestRpcExtensionUIResponseDecodesEachUnionMemberUpstream(t *testing.T) {
	for _, test := range []struct {
		name, wire    string
		value         string // raw JSON of `value`; empty when the member is absent
		confirmed     string // raw JSON of `confirmed`
		cancelled     string // raw JSON of `cancelled`
		wantDialogErr error
	}{
		{name: "value", wire: `{"type":"extension_ui_response","id":"1","value":"b"}`, value: `"b"`},
		{name: "empty value is an answer", wire: `{"type":"extension_ui_response","id":"1","value":""}`, value: `""`},
		{name: "confirmed true", wire: `{"type":"extension_ui_response","id":"1","confirmed":true}`, confirmed: "true", wantDialogErr: context.Canceled},
		{name: "confirmed false", wire: `{"type":"extension_ui_response","id":"1","confirmed":false}`, confirmed: "false", wantDialogErr: context.Canceled},
		{name: "cancelled", wire: `{"type":"extension_ui_response","id":"1","cancelled":true}`, cancelled: "true", wantDialogErr: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			var response rpcUIResponse
			if err := json.Unmarshal([]byte(test.wire), &response); err != nil {
				t.Fatal(err)
			}
			if response.Type != "extension_ui_response" || response.ID != "1" {
				t.Errorf("type/id = %q/%q", response.Type, response.ID)
			}
			if string(response.Value) != test.value || string(response.Confirmed) != test.confirmed || string(response.Cancelled) != test.cancelled {
				t.Errorf("value/confirmed/cancelled = %q/%q/%q, want %q/%q/%q", response.Value, response.Confirmed, response.Cancelled, test.value, test.confirmed, test.cancelled)
			}
			got, err := response.dialogValue()
			if !errors.Is(err, test.wantDialogErr) || string(got) != test.value {
				t.Errorf("dialogValue = %q, %v, want %q, %v", got, err, test.value, test.wantDialogErr)
			}
		})
	}
}

// rpc-mode.ts:769-781: a response resolves the request whose id it carries and removes it from pendingExtensionRequests, so a second response for the
// same id, and a response for an id never requested, change nothing. mutation-checked: resolving the first pending request whatever id the response
// carries, and not decoding the value, each fail it.
func TestRpcExtensionUIResponseResolvesItsRequestOnceUpstream(t *testing.T) {
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
		value, err := ui.Select(context.Background(), "Pick", []string{"a", "b"}, extension.ExtensionUIDialogOptions{})
		done <- result{value, err}
	}()
	id := (<-requests)["id"].(string)

	if ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"never-requested","value":"x"}`)) {
		t.Error("a response for an id that was never requested was handled")
	}
	if !ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + id + `","value":"a"}`)) {
		t.Fatal("the response for the pending request was not handled")
	}
	if got := <-done; got.err != nil || got.value != "a" {
		t.Errorf("select resolved %q, %v, want a, nil", got.value, got.err)
	}
	if ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + id + `","value":"b"}`)) {
		t.Error("a second response for an already resolved request was handled")
	}
}
