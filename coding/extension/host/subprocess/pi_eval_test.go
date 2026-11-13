package subprocess

import (
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// piEval runs body under Node with input as its JSON text and decodes the JSON.stringify of the answer into T with the SDK's string semantics, so a lone
// UTF-16 surrogate crosses as the unit Pi's strings hold (testenv.PiEvalJSON).
func piEval[T any](t testing.TB, body string, input any) T {
	t.Helper()
	var out T
	if err := sdkjson.Unmarshal(testenv.PiEvalJSON(t, body, input), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
