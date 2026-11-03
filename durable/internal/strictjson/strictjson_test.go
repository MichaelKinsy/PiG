package strictjson

import (
	"errors"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// A function a typed value holds reads as chord's copyJson text (packages/chord/src/json.ts: `Value contains a non-JSON
// ${typeof value}; expected strict JSON`, so "non-JSON function"), not as encoding/json's "unsupported type: func()". The
// text reaches a faulted run's submission detail and pi.live.runFailure.detail.
func TestMarshalReportsAFunctionAsChordsNonJSONFunction(t *testing.T) {
	type holder struct {
		Invalid any `json:"invalid"`
	}
	for name, value := range map[string]any{
		"typed member": holder{Invalid: func() {}},
		"map member":   map[string]any{"details": map[string]any{"invalid": func() int { return 1 }}},
		"slice member": []any{"x", func() {}},
		"pointer":      &holder{Invalid: func() {}},
	} {
		_, err := Marshal(value)
		if !errors.Is(err, delta.ErrNotStrictJSON) || err.Error() != "Value contains a non-JSON function; expected strict JSON" {
			t.Fatalf("%s: err = %v, want chord's non-JSON function error", name, err)
		}
	}
}

func TestMarshalReportsANonFiniteNumberAsChordsStrictJSONError(t *testing.T) {
	_, err := Marshal(map[string]any{"level": math.NaN()})
	if !errors.Is(err, delta.ErrNotStrictJSON) || err.Error() != "Value contains a non-finite number and is not strict JSON" {
		t.Fatalf("err = %v, want chord's non-finite error", err)
	}
}

func TestMarshalLeavesAPlainValueAndOtherFailuresAlone(t *testing.T) {
	type holder struct {
		Value any `json:"value"`
	}
	encoded, err := Marshal(holder{Value: "fine"})
	if err != nil || string(encoded) != `{"value":"fine"}` {
		t.Fatalf("Marshal = %s, %v", encoded, err)
	}
	other := errors.New("not an encoding failure")
	if got := Error(other); !errors.Is(got, other) {
		t.Fatalf("Error(%v) = %v, want it unchanged", other, got)
	}
	if got := Error(nil); got != nil {
		t.Fatalf("Error(nil) = %v", got)
	}
	// A channel is not a function: encoding/json's text stays.
	if _, err := Marshal(holder{Value: make(chan int)}); err == nil || errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("channel err = %v, want encoding/json's unsupported type error", err)
	}
}
