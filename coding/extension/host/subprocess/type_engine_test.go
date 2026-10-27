package subprocess

import (
	"encoding/json"
	"strings"
	"testing"
)

// The host answers CallTypeEngine with the registered engine's reply, and with an error naming the missing engine where none is registered (a program that links the
// host without coding/extension/pigsdk).
func TestTypeEngineCallUsesTheRegisteredEngine(t *testing.T) {
	previous := typeEngine.Load()
	t.Cleanup(func() { typeEngine.Store(previous) })
	h := &Host{}
	request := json.RawMessage(`{"member":"Pick","args":[]}`)

	typeEngine.Store(nil)
	if _, err := h.handleTypeEngine(&CallPayload{Method: CallTypeEngine, Args: request}); err == nil || !strings.Contains(err.Error(), "type engine") {
		t.Fatalf("handleTypeEngine without an engine = %v, want an error naming the type engine", err)
	}

	SetTypeEngine(func(got []byte) []byte { return append([]byte(`{"value":`), append(got, '}')...) })
	result, err := h.handleTypeEngine(&CallPayload{Method: CallTypeEngine, Args: request})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"value":` + string(request) + `}`; string(result.Result) != want {
		t.Fatalf("handleTypeEngine = %s, want %s", result.Result, want)
	}
}
