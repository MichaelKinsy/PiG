package testenv

import (
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// PiEvalJSON is NodeJSON for callers that hold Go values: it writes input as the JSON text JSON.parse reads, runs body under the qualified Node and
// returns the JSON.stringify of the answer, for the caller to decode into its own type. Writing the input uses the SDK's JavaScript string semantics,
// so a lone UTF-16 surrogate (WTF-8 in a Go string) crosses as the unit Pi's strings hold, where encoding/json would turn it into U+FFFD; the caller
// decodes the answer with the SDK's Unmarshal for the same reason.
func PiEvalJSON(t testing.TB, body string, input any) []byte {
	t.Helper()
	encoded, err := sdkjson.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return NodeJSON(t, body, encoded)
}
