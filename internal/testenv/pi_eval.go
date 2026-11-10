package testenv

import (
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// PiEval is NodeJSON for callers that hold Go values: it writes input as the JSON text JSON.parse reads, runs body under the qualified Node and decodes
// the JSON.stringify of the answer into T. Both directions use the SDK's JavaScript string semantics, so a lone UTF-16 surrogate (WTF-8 in a Go
// string) crosses as the unit Pi's strings hold, where encoding/json would turn it into U+FFFD.
func PiEval[T any](t testing.TB, body string, input any) T {
	t.Helper()
	encoded, err := sdkjson.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := sdkjson.Unmarshal(NodeJSON(t, body, encoded), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
