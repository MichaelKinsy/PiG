package agent

import (
	stdjson "encoding/json"
	"testing"
)

// Each goEscapes entry is an escape encoding/json writes for a character JSON.stringify writes raw, and the escape decodes to that character: an entry that pairs an escape with any other text would let equalUpToGoEscapes accept an encoding of a different value, and the message would drop the source Pi writes.
func TestGoEscapesAreEncodingJSONEscapesOfTheirCharacter(t *testing.T) {
	for _, e := range goEscapes {
		encoded, err := stdjson.Marshal(e.raw)
		if err != nil {
			t.Fatal(err)
		}
		if want := `"` + e.escape + `"`; string(encoded) != want {
			t.Errorf("encoding/json writes %q as %s, want %s", e.raw, encoded, want)
		}
		var decoded string
		if err := stdjson.Unmarshal([]byte(`"`+e.escape+`"`), &decoded); err != nil || decoded != e.raw {
			t.Errorf("%s decodes to %q (%v), want %q", e.escape, decoded, err, e.raw)
		}
		if len([]rune(e.raw)) != 1 {
			t.Errorf("entry %s pairs the escape with %q, not one character", e.escape, e.raw)
		}
	}
}
