package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestRPCUISelectRoundTrip(t *testing.T) {
	requests := make(chan map[string]any, 1)
	ui := newRPCUIContext(func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var request map[string]any
		if err := json.Unmarshal(data, &request); err != nil {
			t.Fatal(err)
		}
		requests <- request
	})

	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := ui.Select(context.Background(), "Choose", []string{"a", "b"}, extension.ExtensionUIDialogOptions{})
		result <- value
		errs <- err
	}()

	request := <-requests
	if request["type"] != "extension_ui_request" || request["method"] != "select" {
		t.Fatalf("request = %#v", request)
	}
	id, ok := request["id"].(string)
	if !ok || id == "" {
		t.Fatalf("request id = %#v", request["id"])
	}
	response := []byte(`{"type":"extension_ui_response","id":"` + id + `","value":"b"}`)
	if !ui.HandleResponse(response) {
		t.Fatal("matching extension UI response was not handled")
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if got := <-result; got != "b" {
		t.Fatalf("select result = %q, want b", got)
	}
	if ui.HandleResponse(response) {
		t.Fatal("duplicate extension UI response was handled")
	}
}

func TestRPCUICancelAndTimeout(t *testing.T) {
	requests := make(chan map[string]any, 2)
	ui := newRPCUIContext(func(value any) {
		data, _ := json.Marshal(value)
		var request map[string]any
		_ = json.Unmarshal(data, &request)
		requests <- request
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		_, err := ui.Input(ctx, "Input", "value", extension.ExtensionUIDialogOptions{})
		cancelled <- err
	}()
	request := <-requests
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled input error = %v, want context.Canceled", err)
	}
	id := request["id"].(string)
	if ui.HandleResponse([]byte(`{"type":"extension_ui_response","id":"` + id + `","value":"late"}`)) {
		t.Fatal("late response was handled after cancellation")
	}

	started := time.Now()
	// rpc-mode.ts:115-120,143-146: a timed-out dialog resolves its default (false for confirm) like a cancelled one.
	confirmed, err := ui.Confirm(context.Background(), "Confirm", "Continue?", extension.ExtensionUIDialogOptions{Timeout: new(float64(20))})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("timed out confirmation error = %v, want context.Canceled", err)
	}
	if confirmed {
		t.Fatal("timed out confirmation returned true")
	}
	if elapsed := time.Since(started); elapsed < 10*time.Millisecond || elapsed > time.Second {
		t.Fatalf("confirmation timeout elapsed = %v", elapsed)
	}
	<-requests
}

func TestRPCUIFireAndForgetShape(t *testing.T) {
	requests := make(chan map[string]any, 3)
	ui := newRPCUIContext(func(value any) {
		data, _ := json.Marshal(value)
		var request map[string]any
		_ = json.Unmarshal(data, &request)
		requests <- request
	})

	ui.Notify("blocked", "warning")
	ui.SetStatus("review", "running")
	ui.SetEditorText("draft")

	wantMethods := []string{"notify", "setStatus", "set_editor_text"}
	for _, want := range wantMethods {
		request := <-requests
		if request["type"] != "extension_ui_request" || request["method"] != want {
			t.Fatalf("request = %#v, want method %s", request, want)
		}
		if id, _ := request["id"].(string); id == "" {
			t.Fatalf("request has no id: %#v", request)
		}
	}
}

// rpc-mode.ts:115-120,138-150: the request echoes the raw timeout number and any truthy value, including a
// negative or beyond-int32 one, arms a Node timer that runs after about one millisecond.
func TestRPCUIDialogTimeoutIsAJavaScriptNumber(t *testing.T) {
	for _, timeout := range []float64{4294967296.5, -5, 0.5} {
		requests := make(chan map[string]any, 1)
		ui := newRPCUIContext(func(value any) {
			data, _ := json.Marshal(value)
			var request map[string]any
			_ = json.Unmarshal(data, &request)
			requests <- request
		})
		done := make(chan error, 1)
		go func() {
			_, err := ui.Select(context.Background(), "Choose", []string{"a"}, extension.ExtensionUIDialogOptions{Timeout: new(float64(timeout))})
			done <- err
		}()
		if got := (<-requests)["timeout"]; got != timeout {
			t.Errorf("echoed timeout = %v, want %v", got, timeout)
		}
		select {
		case err := <-done:
			// rpc-mode.ts:138-140: a timed-out select resolves undefined, the cancelled form, not "".
			if !errors.Is(err, context.Canceled) {
				t.Errorf("timeout %v: error = %v, want context.Canceled", timeout, err)
			}
		case <-time.After(testbudget.Wait(t)):
			t.Errorf("timeout %v did not dismiss the dialog", timeout)
		}
	}
}

// rpc-types.ts:293-297 RpcExtensionUIResponse is value | confirmed | cancelled:true, and rpc-mode.ts:138-150 and 254-266 read it per dialog: select,
// input and editor give the value unless the response is cancelled (then undefined), confirm gives `confirmed` unless cancelled (then false), and a
// response without a value is undefined for select/input/editor and false for confirm. A cancelled response wins over a value in the same message.
// These dialogs report undefined as context.Canceled, as they do for a cancelled or timed-out dialog; an empty string value is a real answer.
func TestRPCExtensionUIResponseUnionIsReadPerDialog(t *testing.T) {
	type outcome struct {
		value     string
		confirmed bool
		cancelled bool
	}
	run := func(t *testing.T, method string, response string) outcome {
		t.Helper()
		requests := make(chan map[string]any, 1)
		ui := newRPCUIContext(func(value any) {
			data, _ := json.Marshal(value)
			var request map[string]any
			_ = json.Unmarshal(data, &request)
			requests <- request
		})
		result := make(chan outcome, 1)
		go func() {
			var got outcome
			var err error
			switch method {
			case "select":
				got.value, err = ui.Select(context.Background(), "Pick", []string{"a", "b"}, extension.ExtensionUIDialogOptions{})
			case "input":
				got.value, err = ui.Input(context.Background(), "Name", "x", extension.ExtensionUIDialogOptions{})
			case "confirm":
				got.confirmed, err = ui.Confirm(context.Background(), "Sure?", "really", extension.ExtensionUIDialogOptions{})
			case "editor":
				got.value, err = ui.Editor(context.Background(), "Edit", "draft")
			}
			got.cancelled = errors.Is(err, context.Canceled)
			result <- got
		}()
		request := <-requests
		if request["method"] != method {
			t.Fatalf("request method = %v, want %s", request["method"], method)
		}
		message := `{"type":"extension_ui_response","id":"` + request["id"].(string) + `"` + response + `}`
		if !ui.HandleResponse([]byte(message)) {
			t.Fatalf("response %s was not handled", message)
		}
		return <-result
	}

	for _, test := range []struct {
		name, method, response string
		want                   outcome
	}{
		{"select value", "select", `,"value":"b"`, outcome{value: "b"}},
		{"select empty value", "select", `,"value":""`, outcome{value: ""}},
		{"select cancelled", "select", `,"cancelled":true`, outcome{cancelled: true}},
		{"select cancelled beats value", "select", `,"cancelled":true,"value":"b"`, outcome{cancelled: true}},
		{"select with only confirmed is undefined", "select", `,"confirmed":true`, outcome{cancelled: true}},
		{"select with no member is undefined", "select", ``, outcome{cancelled: true}},
		{"select null value is undefined", "select", `,"value":null`, outcome{cancelled: true}},
		{"input value", "input", `,"value":"typed"`, outcome{value: "typed"}},
		{"input empty value", "input", `,"value":""`, outcome{value: ""}},
		{"input cancelled", "input", `,"cancelled":true`, outcome{cancelled: true}},
		{"input with no member is undefined", "input", ``, outcome{cancelled: true}},
		{"editor value", "editor", `,"value":"edited"`, outcome{value: "edited"}},
		{"editor cancelled beats value", "editor", `,"cancelled":true,"value":"edited"`, outcome{cancelled: true}},
		{"editor with no member is undefined", "editor", ``, outcome{cancelled: true}},
		{"confirm yes", "confirm", `,"confirmed":true`, outcome{confirmed: true}},
		{"confirm no", "confirm", `,"confirmed":false`, outcome{}},
		{"confirm cancelled", "confirm", `,"cancelled":true`, outcome{cancelled: true}},
		{"confirm cancelled beats confirmed", "confirm", `,"cancelled":true,"confirmed":true`, outcome{cancelled: true}},
		{"confirm with only a value is false", "confirm", `,"value":"yes"`, outcome{}},
		{"confirm with no member is false", "confirm", ``, outcome{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := run(t, test.method, test.response); got != test.want {
				t.Fatalf("%s %s => %+v, want %+v", test.method, test.response, got, test.want)
			}
		})
	}
}
