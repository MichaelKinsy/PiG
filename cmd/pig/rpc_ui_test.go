package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
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
		value, err := ui.Select(context.Background(), "Choose", []string{"a", "b"}, nil)
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
		_, err := ui.Input(ctx, "Input", "value", nil)
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
	confirmed, err := ui.Confirm(context.Background(), "Confirm", "Continue?", map[string]any{"timeout": 20})
	if err != nil {
		t.Fatal(err)
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
