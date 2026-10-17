package subprocess

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A Provider object's provider.callback names the request callback it invokes; the host answers each by its name and refuses a name the caller did not pass, as a Node Provider's options object has no such member (provider-object.mjs dispatchProviderObjectCallback "Provider callback ... is absent").
func TestNativeStreamCallbackDispatchesByName(t *testing.T) {
	model := &ai.Model{ID: "callback-model"}
	var calls []string
	options := ai.StreamOptions{
		OnPayload: func(payload any, requested *ai.Model) (any, error) {
			calls = append(calls, "payload")
			if requested != model {
				t.Errorf("onPayload model = %p, want %p", requested, model)
			}
			return map[string]any{"replaced": payload}, nil
		},
		OnResponse: func(_ context.Context, response ai.ProviderResponse, _ *ai.Model) error {
			calls = append(calls, "response")
			if response.Status != 201 || response.Headers["x-a"] != "b" {
				t.Errorf("onResponse response = %#v", response)
			}
			return nil
		},
	}
	callback := nativeStreamCallback(t.Context(), options, model)
	result, err := callback(json.RawMessage(`{"method":"onResponse","params":{"value":{"status":201,"headers":{"x-a":"b"}}}}`))
	if err != nil || string(result) != "null" {
		t.Fatalf("onResponse = %s, %v", result, err)
	}
	result, err = callback(json.RawMessage(`{"method":"onPayload","params":{"value":{"n":1}}}`))
	if err != nil || string(result) != `{"replaced":{"n":1}}` {
		t.Fatalf("onPayload = %s, %v", result, err)
	}
	for _, method := range []string{"onProviderStreamEvent", "transformHeaders", "prompt"} {
		if _, err := callback(json.RawMessage(`{"method":"` + method + `","params":{"value":{}}}`)); err == nil || err.Error() != "Provider callback "+method+" is absent" {
			t.Errorf("%s: err = %v, want the absent-callback error", method, err)
		}
	}
	if len(calls) != 2 || calls[0] != "response" || calls[1] != "payload" {
		t.Fatalf("calls = %v", calls)
	}
	if names := nativeStreamCallbackNames(options); len(names) != 2 || names[0] != "onPayload" || names[1] != "onResponse" {
		t.Fatalf("declared callbacks = %v", names)
	}
}
