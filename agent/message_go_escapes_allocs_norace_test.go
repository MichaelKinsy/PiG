//go:build !race

// The race detector makes sync.Pool drop a quarter of the values put back, at random, so the pooled encoder state of encoding/json is allocated afresh on
// some runs and an allocation count differs from run to run. This ratchet runs without it, as the repository's other allocation ratchets do.

package agent

import (
	"strings"
	"testing"
)

var systemMessageSink AgentMessage

// A system message whose text holds characters encoding/json escapes (the <tools> section of every system prompt) costs no more to decode than one without them.
func TestSystemMessageWithEscapedCharactersDecodesAtTheCostOfOneWithout(t *testing.T) {
	escaped := canonicalSystemMessageText(t, strings.Repeat("<tools>\n- read & write\n</tools>\n", 20))
	plain := canonicalSystemMessageText(t, strings.Repeat("(tools)\n- read + write\n(/tools)\n", 20))
	allocs := func(data []byte) float64 {
		return testing.AllocsPerRun(20, func() {
			systemMessageSink = AgentMessage{}
			_ = systemMessageSink.UnmarshalJSON(data)
		})
	}
	if e, p := allocs(escaped), allocs(plain); e > p+2 {
		t.Fatalf("decoding with escaped characters = %v allocations, without = %v", e, p)
	}
}
