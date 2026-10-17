package sdk

import (
	"math"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The Node SDK sends an events.emit payload as JSON.stringify writes it, so NaN and the infinities are null; the Go SDK encodes the payload before the call and must not fail the emit instead.
func TestEventBusEmitWritesNonFiniteNumbersAsNull(t *testing.T) {
	ext := New("bus")
	emitted := make(chan error, 1)
	ext.Command("go", "", func(ctx Context, _ string) error {
		emitted <- ctx.Events().Emit("ch", map[string]any{"n": math.NaN(), "l": []any{math.Inf(-1), 1}})
		return nil
	})
	host, done := startBusExtension(t, ext)
	readyBusExtension(t, host)
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "cmd1", Request: &requestMsg{Method: "command", Tool: "go"}})
	args, _ := readBusCall(t, host, "events.emit", "null")
	if string(args.JSON) != `{"l":[null,1],"n":null}` {
		t.Fatalf("events.emit payload = %s, want the non-finite numbers as null", args.JSON)
	}
	if err := <-emitted; err != nil {
		t.Fatal(err)
	}
	stopBusExtension(t, host, done)
}

// A wire value whose own MarshalJSON encodes its members (an OrderedObject) is part of the frame: its non-finite numbers are null too.
func TestOrderedWireValuesWriteNonFiniteNumbersAsNull(t *testing.T) {
	encoded, err := json.MarshalJS(ToolResult{Content: "x", Details: NewOrderedObject("b", math.NaN(), "a", math.Inf(1))})
	if err != nil {
		t.Fatal(err)
	}
	if want := `"details":{"b":null,"a":null}`; !strings.Contains(string(encoded), want) {
		t.Fatalf("tool result = %s, want %s", encoded, want)
	}
}
