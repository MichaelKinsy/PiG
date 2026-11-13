package subprocess

import (
	"context"
	"encoding/json"
	"testing"
)

// An extension's toolChoice object is the caller's own object: Pi hands it to the provider, which writes its members in the order the
// extension built them (anthropic-messages.ts:1587-1593). The bridge hands on the JSON text the extension sent; a name stays a string.
func TestModelStreamRequestKeepsToolChoiceText(t *testing.T) {
	for _, sent := range []string{`{"type":"tool","name":"read"}`, `{"name":"read","type":"tool"}`} {
		request, err := decodeModelStreamRequest(json.RawMessage(`{"messages":[],"toolChoice":` + sent + `}`))
		if err != nil {
			t.Fatal(err)
		}
		if raw, ok := request["toolChoice"].(json.RawMessage); !ok || string(raw) != sent {
			t.Fatalf("toolChoice = %#v, want the text %s", request["toolChoice"], sent)
		}
	}
	for raw, want := range map[string]any{`{"toolChoice":"auto"}`: "auto", `{"toolChoice":null}`: nil, `{}`: nil} {
		request, err := decodeModelStreamRequest(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if request["toolChoice"] != want {
			t.Fatalf("%s: toolChoice = %#v, want %#v", raw, request["toolChoice"], want)
		}
	}
}

// complete() carries the same request, so its toolChoice object keeps its text too.
func TestCompleteRequestKeepsToolChoiceText(t *testing.T) {
	const sent = `{"name":"read","type":"tool"}`
	var got map[string]any
	actions := &HostCallbacks{Complete: func(_ context.Context, _, request, _ map[string]any) (map[string]any, error) {
		got = request
		return map[string]any{"stopReason": "stop", "content": []any{}}, nil
	}}
	if _, err := (&UIBridge{}).handleComplete(t.Context(), actions, json.RawMessage(`{"model":{},"request":{"messages":[],"toolChoice":`+sent+`}}`)); err != nil {
		t.Fatal(err)
	}
	if raw, ok := got["toolChoice"].(json.RawMessage); !ok || string(raw) != sent {
		t.Fatalf("toolChoice = %#v, want the text %s", got["toolChoice"], sent)
	}
}
