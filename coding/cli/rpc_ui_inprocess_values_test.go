package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// An in-process extension in RPC mode reaches the RPC UI through the runner's prompt wrapper: a client's non-string `value` and non-boolean `confirmed` arrive as the
// client's JSON, as they do for an SDK extension and in Pi.
func TestRPCUIInProcessExtensionGetsTheClientsJSON(t *testing.T) {
	requests := make(chan map[string]any, 1)
	ui := newRPCUIContext(func(value any) {
		data, _ := json.Marshal(value)
		var request map[string]any
		_ = json.Unmarshal(data, &request)
		requests <- request
	})
	r := inproc.NewRunner(nil, ".")
	r.SetUIContext(ui, extension.ModeRPC)
	wrapped, err := extension.FromContext(r.DispatchContext(context.Background())).UI()
	if err != nil {
		t.Fatal(err)
	}
	values, ok := wrapped.(extension.DialogValueUIContext)
	if !ok {
		t.Fatal("the UI handed to an in-process extension is not an extension.DialogValueUIContext")
	}
	for _, test := range []struct {
		members string
		call    func() (json.RawMessage, error)
		want    string
	}{
		{`"value":5`, func() (json.RawMessage, error) {
			return values.SelectValue(context.Background(), "T", []string{"a"}, extension.ExtensionUIDialogOptions{})
		}, `5`},
		{`"value":null`, func() (json.RawMessage, error) {
			return values.InputValue(context.Background(), "T", nil, extension.ExtensionUIDialogOptions{})
		}, `null`},
		{`"confirmed":"yes"`, func() (json.RawMessage, error) {
			return values.ConfirmValue(context.Background(), "T", "m", extension.ExtensionUIDialogOptions{})
		}, `"yes"`},
		{`"value":{"a":[1]}`, func() (json.RawMessage, error) { return values.EditorValue(context.Background(), "T", nil) }, `{"a":[1]}`},
	} {
		type outcome struct {
			value json.RawMessage
			err   error
		}
		done := make(chan outcome, 1)
		go func() {
			value, err := test.call()
			done <- outcome{value, err}
		}()
		id := (<-requests)["id"].(string)
		if !ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + id + `",` + test.members + `}`)) {
			t.Fatalf("%s: response not handled", test.members)
		}
		if got := <-done; got.err != nil || string(got.value) != test.want {
			t.Errorf("%s: resolved %s, %v, want %s", test.members, got.value, got.err, test.want)
		}
	}
}
