package subprocess

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// valueUI answers every dialog with the JSON in reply, as a UIContext whose dialogs resolve with the client's JSON (Pi's RPC mode) does.
type valueUI struct {
	*mockUIContext
	reply json.RawMessage
	err   error
}

func (u valueUI) SelectValue(context.Context, string, []string, extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.reply, u.err
}
func (u valueUI) ConfirmValue(context.Context, string, string, extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.reply, u.err
}
func (u valueUI) InputValue(context.Context, string, *string, extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.reply, u.err
}
func (u valueUI) EditorValue(context.Context, string, *string) (json.RawMessage, error) {
	return u.reply, u.err
}

// rpc-mode.ts resolves a dialog with whatever JSON the client sent, so the bridge's reply to an SDK carries that JSON: a number, null, an object, an array.
// An undefined answer (context.Canceled) is ok false; a confirm with no answer is false. mutation-checked: marshalling the value through a string fails it.
func TestUIBridge_DialogRepliesCarryTheClientsJSON(t *testing.T) {
	for _, test := range []struct {
		method, args, member string
		ui                   valueUI
		want                 string
	}{
		{"ui.select", `{"title":"T","options":["a"]}`, "selected", valueUI{reply: json.RawMessage(`5`)}, `{"ok":true,"selected":5}`},
		{"ui.select", `{"title":"T","options":["a"]}`, "selected", valueUI{reply: json.RawMessage(`null`)}, `{"ok":true,"selected":null}`},
		{"ui.select", `{"title":"T","options":["a"]}`, "selected", valueUI{err: context.Canceled}, `{"ok":false,"selected":""}`},
		{"ui.input", `{"title":"T","placeholder":"p"}`, "text", valueUI{reply: json.RawMessage(`{"a":[1,"x"]}`)}, `{"ok":true,"text":{"a":[1,"x"]}}`},
		{"ui.editor", `{"title":"T","prefill":"p"}`, "text", valueUI{reply: json.RawMessage(`[1,2]`)}, `{"ok":true,"text":[1,2]}`},
		{"ui.confirm", `{"title":"T","message":"m"}`, "confirmed", valueUI{reply: json.RawMessage(`"yes"`)}, `{"confirmed":"yes"}`},
		{"ui.confirm", `{"title":"T","message":"m"}`, "confirmed", valueUI{reply: json.RawMessage(`null`)}, `{"confirmed":null}`},
		{"ui.confirm", `{"title":"T","message":"m"}`, "confirmed", valueUI{err: context.Canceled}, `{"confirmed":false}`},
	} {
		test.ui.mockUIContext = &mockUIContext{}
		res, err := call(newTestBridge(test.ui), test.method, test.args)
		if err != nil {
			t.Fatal(err)
		}
		if string(res.Result) != test.want {
			t.Errorf("%s reply = %s, want %s", test.method, res.Result, test.want)
		}
	}
}
