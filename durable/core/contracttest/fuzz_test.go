package contracttest

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// The model fuzzed against itself: whatever JSON text it accepts, it writes JSON that decodes to the same value, writes it
// compactly, and writes the same bytes when given its own output.
func FuzzReferenceEncode(f *testing.F) {
	AddEncodeSeeds(f)
	f.Fuzz(func(t *testing.T, in []byte) {
		out, err := ReferenceEncode(in)
		if err != nil {
			t.Skip()
		}
		var a, b any
		if json.Unmarshal(in, &a) != nil {
			t.Skip() // beyond Go's decoder: 1e999, lone surrogates in strings
		}
		if err := json.Unmarshal(out, &b); err != nil {
			t.Fatalf("output %q is not JSON: %v", out, err)
		}
		if !reflect.DeepEqual(noNegativeZero(a), noNegativeZero(b)) {
			t.Fatalf("in %q decodes to %v, out %q to %v", in, a, out, b)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, out); err != nil || compact.String() != string(out) {
			t.Fatalf("output %q is not compact", out)
		}
		again, err := ReferenceEncode(out)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("encoding the output %q again gives %q (%v)", out, again, err)
		}
	})
}

// The kit against the models: it accepts ReferenceEncode and ReferenceIndex on every vector, and a broken encoder and a
// substring-search scanner are caught by the same comparison the fuzz bodies use.
func TestFuzzKitAcceptsTheModelsAndCatchesBrokenOnes(t *testing.T) {
	for _, v := range EncodeVectors(t) {
		if skip, err := encodeDifference(ReferenceEncode, []byte(v.In)); skip || err != nil {
			t.Errorf("model rejected by the kit on %q: skip=%v err=%v", v.In, skip, err)
		}
	}
	for _, v := range ScanVectors(t) {
		if skip, err := scanDifference(ReferenceIndex, []byte(v.Record)); skip || err != nil {
			t.Errorf("model rejected by the kit on %q: skip=%v err=%v", v.Record, skip, err)
		}
	}
	// An encoder that sorts no keys and prints -0 as -0.
	sloppy := func(in []byte) ([]byte, error) { return bytes.ReplaceAll(in, []byte(" "), nil), nil }
	caught := 0
	for _, v := range EncodeVectors(t) {
		if _, err := encodeDifference(sloppy, []byte(v.In)); err != nil {
			caught++
		}
	}
	if caught < 100 {
		t.Errorf("the kit caught a whitespace-stripping encoder on only %d vectors", caught)
	}
	if _, err := scanDifference(func([]byte) (Index, error) { return Index{}, nil }, []byte(`{"kind":"pi.user","id":1,"conversationId":1}`)); err == nil {
		t.Error("the kit accepted a scanner that returns nothing")
	}
}
