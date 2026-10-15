package subprocess

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// An extension's requestMetadata is a JavaScript object whose key order Pi's Bedrock request keeps (bedrock-converse-stream.ts passes
// options.requestMetadata to the SDK, which writes it in object order). The bridge hands it on as an ordered object.
func TestModelStreamRequestKeepsRequestMetadataOrder(t *testing.T) {
	request, err := decodeModelStreamRequest(json.RawMessage(`{"messages":[],"requestMetadata":{"zeta":"1","alpha":"2","10":"3"}}`))
	if err != nil {
		t.Fatal(err)
	}
	object, ok := request["requestMetadata"].(*orderedjson.Object)
	if !ok {
		t.Fatalf("requestMetadata = %#v, want an ordered object", request["requestMetadata"])
	}
	if keys := object.Keys(); !slices.Equal(keys, []string{"10", "zeta", "alpha"}) {
		t.Fatalf("keys = %q", keys)
	}
	// A null or non-object requestMetadata is left for the option reader to judge.
	for _, raw := range []string{`{"requestMetadata":null}`, `{"requestMetadata":"x"}`} {
		request, err := decodeModelStreamRequest(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, ordered := request["requestMetadata"].(*orderedjson.Object); ordered {
			t.Fatalf("%s: requestMetadata became an ordered object", raw)
		}
	}
}

// The host sends requestMetadata to an extension provider in the same order.
func TestProviderOptionsWireKeepsRequestMetadataOrder(t *testing.T) {
	encoded, err := json.Marshal(providerOptionsWire(ai.StreamOptions{RequestMetadata: map[string]string{"zeta": "1", "alpha": "2", "mid": "4", "10": "3"}, RequestMetadataOrder: []string{"zeta", "missing", "alpha", "zeta"}}))
	if err != nil {
		t.Fatal(err)
	}
	var options struct {
		RequestMetadata json.RawMessage `json:"requestMetadata"`
	}
	if err := json.Unmarshal(encoded, &options); err != nil {
		t.Fatal(err)
	}
	if got, want := string(options.RequestMetadata), `{"10":"3","zeta":"1","alpha":"2","mid":"4"}`; got != want {
		t.Fatalf("requestMetadata = %s, want %s", got, want)
	}
}

// complete() carries the same request, so its requestMetadata keeps the order too.
func TestCompleteRequestKeepsRequestMetadataOrder(t *testing.T) {
	var got map[string]any
	actions := &HostCallbacks{Complete: func(_ context.Context, _, request, _ map[string]any) (map[string]any, error) {
		got = request
		return map[string]any{"stopReason": "stop", "content": []any{}}, nil
	}}
	if _, err := (&UIBridge{}).handleComplete(t.Context(), actions, json.RawMessage(`{"model":{},"request":{"messages":[],"requestMetadata":{"zeta":"1","alpha":"2"}}}`)); err != nil {
		t.Fatal(err)
	}
	object, ok := got["requestMetadata"].(*orderedjson.Object)
	if !ok || !slices.Equal(object.Keys(), []string{"zeta", "alpha"}) {
		t.Fatalf("requestMetadata = %#v", got["requestMetadata"])
	}
	if _, err := (&UIBridge{}).handleComplete(t.Context(), actions, json.RawMessage(`{"model":{},"request":null}`)); err != nil || got != nil {
		t.Fatalf("null request: err = %v, request = %#v", err, got)
	}
}
