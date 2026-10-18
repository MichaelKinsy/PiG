package host

// pi: packages/chord/src/schema.ts
// Schema cases of Pi schema.ts. The review.test.ts "#11 fits" cases live in review_upstream_test.go.

import (
	"math"
	"testing"
)

// Pi schema.ts fits: an integer fits a number, not the reverse.
func TestFitsAnIntegerFitsANumberNotTheReverse(t *testing.T) {
	if !Fits(Schema{"type": "integer"}, tNumber()) || Fits(tNumber(), Schema{"type": "integer"}) {
		t.Fatal("integer and number")
	}
}

const piHashOfASmiley = "1c8ecbf938b0dec5"

// canonicalJson sorts keys by UTF-16 code units and writes numbers as JSON.stringify does (Pi schema.ts).
func TestCanonicalJSONMatchesJavaScript(t *testing.T) {
	value := map[string]any{"\U0001F600": 1, "\uFF61": 2, "b": []any{1e21, math.Copysign(0, -1), 0.1, "\u2028<&>"}, "a": nil, "u": absent{}}
	// Pi: canonicalJson({"😀":1,"｡":2,b:[1e21,-0,0.1,"\u2028<&>"],a:null,u:undefined})
	want := "{\"a\":null,\"b\":[1e+21,0,0.1,\"\u2028<&>\"],\"\U0001F600\":1,\"\uFF61\":2}"
	if got := CanonicalJSON(value); got != want {
		t.Fatalf("canonical %s, want %s", got, want)
	}
	// Pi: hash64("a😀") = hash over the UTF-16 code units.
	if got := Hash64("a\U0001F600"); got != piHashOfASmiley {
		t.Fatalf("hash64 %s, Pi %s", got, piHashOfASmiley)
	}
}
