package session

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// The host answers StartPhase with `{span, carrier?}`; its span is the host's own uint32 handle, so an answer that is no such
// handle (a negative, a number past 2^32-1, NaN, a string) names no span instead of an implementation-defined conversion.
func TestPhaseAnsweredTakesOnlyAUint32SpanHandle(t *testing.T) {
	for _, tc := range []struct {
		name string
		span any
		want uint32
	}{
		{"a handle", float64(7), 7},
		{"the largest handle", float64(4294967295), 4294967295},
		{"zero", float64(0), 0},
		{"a negative", float64(-1), 0},
		{"past the range", float64(4294967296), 0},
		{"not a number", "7", 0},
		{"absent", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := &invocation{start: func() {}, signal: &Cancel{}}
			p := &PhaseInfo{inv: inv}
			answer := jv.NewObject()
			if tc.span != nil {
				answer.Set("span", tc.span)
			}
			p.Answered(answer, nil)
			if inv.span != tc.want || inv.signal.Span != tc.want {
				t.Fatalf("span %d, want %d", inv.span, tc.want)
			}
		})
	}
}
