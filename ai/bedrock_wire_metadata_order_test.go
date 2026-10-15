//go:build !pig_strip_bedrock_converse_stream

package ai

import "testing"

// A JavaScript object enumerates array-index keys first in ascending order, then the other keys in insertion order. requestMetadata
// inserts the RequestMetadataOrder keys first and the rest sorted, so an index key listed late or missing from the order still comes first.
func TestBedrockStringMapEnumeratesLikeAJavaScriptObject(t *testing.T) {
	values := map[string]string{"zeta": "1", "10": "2", "alpha": "3", "5": "4", "mid": "5"}
	for _, tc := range []struct {
		order []string
		want  string
	}{
		{nil, `{"5":"4","10":"2","alpha":"3","mid":"5","zeta":"1"}`},
		{[]string{"zeta", "10", "missing", "zeta"}, `{"5":"4","10":"2","zeta":"1","alpha":"3","mid":"5"}`},
		{[]string{"mid", "zeta", "alpha"}, `{"5":"4","10":"2","mid":"5","zeta":"1","alpha":"3"}`},
	} {
		wire := &bedrockWire{}
		bedrockStringMap(wire, values, tc.order, (*bedrockWire).str)
		if wire.err != nil || string(wire.buf) != tc.want {
			t.Errorf("order %q: got %s (%v), want %s", tc.order, wire.buf, wire.err, tc.want)
		}
	}
}
