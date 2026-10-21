package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// rpc-mode.ts:137-150 resolves select and input with `"cancelled" in r && r.cancelled ? undefined : "value" in r ? r.value : undefined` and confirm with
// `... ? false : "confirmed" in r ? r.confirmed : false`: the client's JSON is passed through unchecked, `cancelled` is read by JavaScript truthiness, and `null` is a value.
// mutation-checked: decoding value into *string, confirmed into bool, or cancelled into bool each fail it.
func TestRPCUIResponsePassesTheClientsJSONThroughLikePi(t *testing.T) {
	type call func(ui *rpcUIContext) (json.RawMessage, error)
	selectCall := func(ui *rpcUIContext) (json.RawMessage, error) {
		return ui.SelectValue(context.Background(), "T", []string{"a"}, extension.ExtensionUIDialogOptions{})
	}
	confirmCall := func(ui *rpcUIContext) (json.RawMessage, error) {
		return ui.ConfirmValue(context.Background(), "T", "m", extension.ExtensionUIDialogOptions{})
	}
	inputCall := func(ui *rpcUIContext) (json.RawMessage, error) {
		return ui.InputValue(context.Background(), "T", new("p"), extension.ExtensionUIDialogOptions{})
	}
	editorCall := func(ui *rpcUIContext) (json.RawMessage, error) {
		return ui.EditorValue(context.Background(), "T", new("p"))
	}
	for _, test := range []struct {
		name, members string
		call          call
		want          string // raw JSON the extension observes
		cancelled     bool   // undefined
	}{
		{"select number", `"value":5`, selectCall, `5`, false},
		{"select null", `"value":null`, selectCall, `null`, false},
		{"select object", `"value":{"a":[1,"x"]}`, selectCall, `{"a":[1,"x"]}`, false},
		{"select bool", `"value":false`, selectCall, `false`, false},
		{"select empty string", `"value":""`, selectCall, `""`, false},
		{"select absent value", `"confirmed":true`, selectCall, ``, true},
		{"select cancelled true", `"cancelled":true,"value":5`, selectCall, ``, true},
		{"select cancelled truthy string", `"cancelled":"no","value":5`, selectCall, ``, true},
		{"select cancelled truthy empty object", `"cancelled":{},"value":5`, selectCall, ``, true},
		{"select cancelled falsy zero", `"cancelled":0,"value":5`, selectCall, `5`, false},
		{"select cancelled falsy null", `"cancelled":null,"value":5`, selectCall, `5`, false},
		{"input array", `"value":[1,2]`, inputCall, `[1,2]`, false},
		{"editor number", `"value":7.5`, editorCall, `7.5`, false},
		{"confirm string", `"confirmed":"yes"`, confirmCall, `"yes"`, false},
		{"confirm null", `"confirmed":null`, confirmCall, `null`, false},
		{"confirm zero", `"confirmed":0`, confirmCall, `0`, false},
		{"confirm absent", `"value":"x"`, confirmCall, `false`, false},
		{"confirm cancelled truthy", `"cancelled":1,"confirmed":true`, confirmCall, `false`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			ui := newRPCUIContext(func(value any) {
				data, _ := json.Marshal(value)
				var request map[string]any
				_ = json.Unmarshal(data, &request)
				requests <- request
			})
			type outcome struct {
				value json.RawMessage
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				value, err := test.call(ui)
				done <- outcome{value, err}
			}()
			id := (<-requests)["id"].(string)
			if !ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + id + `",` + test.members + `}`)) {
				t.Fatal("the response was not handled")
			}
			got := <-done
			if test.cancelled {
				// A cancelled dialog is Pi's undefined (confirm: false); the host reports it as context.Canceled and the bridge answers the default.
				if !errors.Is(got.err, context.Canceled) {
					t.Fatalf("err = %v, want context.Canceled", got.err)
				}
				return
			}
			if got.err != nil || string(got.value) != test.want {
				t.Fatalf("resolved %s, %v, want %s", got.value, got.err, test.want)
			}
		})
	}
}

// The string-typed UIContext methods cannot hold a non-string answer: it resolves as undefined (context.Canceled) instead of being dropped, so the dialog does not stay pending.
func TestRPCUIStringMethodsResolveANonStringAnswerAsUndefined(t *testing.T) {
	requests := make(chan map[string]any, 1)
	ui := newRPCUIContext(func(value any) {
		data, _ := json.Marshal(value)
		var request map[string]any
		_ = json.Unmarshal(data, &request)
		requests <- request
	})
	done := make(chan error, 1)
	go func() {
		_, err := ui.Select(context.Background(), "T", []string{"a"}, extension.ExtensionUIDialogOptions{})
		done <- err
	}()
	id := (<-requests)["id"].(string)
	if !ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + id + `","value":5}`)) {
		t.Fatal("a wrong-typed value was dropped")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
