// SPDX-License-Identifier: MIT

package history

import (
	"errors"
	"math"
	"testing"
)

// TestCanonicalMatchesJSONStringify checks Canonical against JSON.stringify(JSON.parse(text)) on the corpus
// oracle/gen_canon.mjs wrote (CONTRACT section 5.6, encoder differential).
func TestCanonicalMatchesJSONStringify(t *testing.T) {
	cases := corpusLines(t, "canon.hex")
	if len(cases) < 2000 {
		t.Fatalf("corpus has %d cases", len(cases))
	}
	for n, c := range cases {
		in := unhex(t, c[0])
		got, err := Canonical(nil, in)
		if c[1] == "-" {
			if err == nil {
				t.Fatalf("case %d: %q accepted as %q, JSON.parse rejects it", n, in, got)
			}
			continue
		}
		want := unhex(t, c[1])
		if err != nil {
			t.Fatalf("case %d: %q: %v", n, in, err)
		}
		if string(got) != string(want) {
			t.Fatalf("case %d: %q\n got  %q\n want %q", n, in, got, want)
		}
	}
}

// TestCanonicalBeyondDoubleRange: JSON.stringify(JSON.parse("1e999")) is "null" (conformance vectors), which package jv
// prints; AppendNumber, used for clock values the core writes, rejects a non-finite number.
func TestCanonicalBeyondDoubleRange(t *testing.T) {
	for in, want := range map[string]string{`1e999`: `null`, `-1e999`: `null`, `{"a":[1e400,2]}`: `{"a":[null,2]}`} {
		got, err := Canonical(nil, []byte(in))
		if err != nil || string(got) != want {
			t.Errorf("Canonical(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
	if _, err := AppendNumber(nil, math.Inf(1)); !errors.Is(err, ErrNonFinite) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := AppendNumber(nil, 1e21); string(got) != "1e+21" {
		t.Fatalf("AppendNumber(1e21) = %s", got)
	}
}

// TestCanonicalIsIdempotentOnMalformedUTF8 pins inputs FuzzCanonical found: bytes that only look like a WTF-8
// surrogate, and surrogate escapes that pair up.
func TestCanonicalIsIdempotentOnMalformedUTF8(t *testing.T) {
	for _, in := range []string{"\"\\b\xed\xcc0\"", `"\ud9b1\udc71"`, `"\udc71\ud9b1"`} {
		out, err := Canonical(nil, []byte(in))
		if err != nil {
			t.Fatal(err)
		}
		again, err := Canonical(nil, out)
		if err != nil || string(again) != string(out) {
			t.Fatalf("%q -> %q -> %q (%v)", in, out, again, err)
		}
	}
	if out, _ := Canonical(nil, []byte(`"\ud800"`)); string(out) != `"\ud800"` {
		t.Fatalf("a lone surrogate encodes as %s", out)
	}
}
