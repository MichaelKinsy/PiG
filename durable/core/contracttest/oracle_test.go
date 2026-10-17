package contracttest

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// failures records what a Check function reports, so a test can require that a broken implementation is caught.
type failures struct{ n int }

func (f *failures) Helper()                           {}
func (f *failures) Errorf(string, ...any)             { f.n++ }
func (f *failures) Fatalf(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }

func TestVectorFamiliesAreNotEmptyAndAreTheDocumentedSize(t *testing.T) {
	if got := Summary(t); got != "encode 458, scan 300, chord 214, args 180, uuid 30" {
		t.Fatalf("vector counts changed (regenerate with durable/contract/tools/gen-oracle.mjs and update this expectation from its output): %s", got)
	}
}

// ---- scan: a reference extraction by decoding, which the byte scanner must equal without decoding.

func TestScanVectorsAgreeWithADecodingExtraction(t *testing.T) {
	CheckScan(t, ReferenceIndex)
}

// A scanner that looks for the first `"kind":"` and `"role":"` substring is the bug the vectors exist to catch: it reads a
// look-alike key inside tool arguments or an unknown field, and misses keys written with escapes or in another order.
func TestAnIndexBySubstringSearchFailsTheScanVectors(t *testing.T) {
	var f failures
	CheckScan(&f, func(record []byte) (Index, error) {
		ix, err := ReferenceIndex(record)
		if err != nil {
			return ix, err
		}
		s := string(record)
		if _, rest, ok := strings.Cut(s, `"kind":"`); ok {
			ix.Kind, _, _ = strings.Cut(rest, `"`)
		}
		if _, rest, ok := strings.Cut(s, `"role":"`); ok {
			ix.Role, _, _ = strings.Cut(rest, `"`)
		}
		return ix, nil
	})
	if f.n == 0 {
		t.Fatal("the scan vectors do not separate a substring search from a scanner")
	}
}

// ---- encode: the vectors' outputs are what a decoder reads back as the inputs' values.

func TestEncodeOutputsDecodeToTheInputsValues(t *testing.T) {
	for i, v := range EncodeVectors(t) {
		var in, out any
		if err := json.Unmarshal([]byte(v.In), &in); err != nil {
			// A number beyond the double range is Infinity in JavaScript and null in the output; Go's decoder rejects it.
			if strings.Contains(v.In, "e999") && strings.Contains(v.Out, "null") || v.Out == "null" {
				continue
			}
			t.Fatalf("vector %d input %q: %v", i, v.In, err)
		}
		if err := json.Unmarshal([]byte(v.Out), &out); err != nil {
			t.Fatalf("vector %d output %q: %v", i, v.Out, err)
		}
		if fmt.Sprint(noNegativeZero(in)) != fmt.Sprint(noNegativeZero(out)) {
			t.Errorf("vector %d: %q decodes to %v, output %q decodes to %v", i, v.In, in, v.Out, out)
		}
		if strings.ContainsAny(v.Out, "\n\t") || strings.Contains(v.Out, ": ") && !strings.Contains(v.Out, `"`) {
			t.Errorf("vector %d output is not compact: %q", i, v.Out)
		}
	}
}

// JSON.stringify prints -0 as 0, so the decoded values equal except for the sign of zero.
func noNegativeZero(v any) any {
	switch x := v.(type) {
	case float64:
		if x == 0 {
			return float64(0)
		}
	case []any:
		for i := range x {
			x[i] = noNegativeZero(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = noNegativeZero(x[k])
		}
	}
	return v
}

func TestAnEncoderThatReturnsItsInputFailsTheEncodeVectors(t *testing.T) {
	var f failures
	CheckEncode(&f, func(in []byte) ([]byte, error) { return in, nil })
	if f.n < 100 {
		t.Fatalf("only %d of 458 vectors reject an identity encoder: the vectors do not exercise number format, escapes and key order", f.n)
	}
}

func TestEncodeVectorsCoverTheJavaScriptTraps(t *testing.T) {
	want := map[string]string{
		`{"b":1,"2":2,"1":3}`: `{"1":3,"2":2,"b":1}`,
		`{"a":1,"a":2}`:       `{"a":2}`,
		`[-0,0,-0.0]`:         `[0,0,0]`,
		`"\ud800"`:            `"\ud800"`,
		`"\u2028\u2029"`:      "\"\u2028\u2029\"",
		`[1e21,1e-7,123456789012345678901234567890]`: `[1e+21,1e-7,1.2345678901234568e+29]`,
	}
	got := map[string]string{}
	for _, v := range EncodeVectors(t) {
		got[v.In] = v.Out
	}
	for in, out := range want {
		if got[in] != out {
			t.Errorf("vector %q: want %q, have %q", in, out, got[in])
		}
	}
}

// ---- chord and args: the vectors parse and carry the shapes the core's tests rely on.

func TestChordVectorsHaveOpsAndLongStrings(t *testing.T) {
	var long, replace int
	for i, v := range ChordVectors(t) {
		var ops [][]json.RawMessage
		if err := json.Unmarshal(v.Ops, &ops); err != nil {
			t.Fatalf("vector %d ops: %v", i, err)
		}
		if len(v.Before) > 65536 {
			long++
		}
		for _, op := range ops {
			if string(op[0]) == `"r"` {
				replace++
			}
		}
	}
	if long < 4 || replace == 0 {
		t.Fatalf("long-string vectors %d, replace ops %d", long, replace)
	}
}

func TestArgsVectorsHaveBothOutcomesAndBothSchemaKinds(t *testing.T) {
	var ok, bad, plain, reordered int
	for _, v := range ArgsVectors(t) {
		switch {
		case v.OK != nil:
			ok++
			var in, out map[string]json.RawMessage
			_ = json.Unmarshal(v.Call.Arguments, &in)
			_ = json.Unmarshal([]byte(*v.OK), &out)
			if v.PlainSchema && keyOrder(string(v.Call.Arguments)) != keyOrder(*v.OK) {
				reordered++
			}
		case v.Error != nil:
			bad++
		default:
			t.Fatal("a vector with neither ok nor error")
		}
		if v.PlainSchema {
			plain++
		}
	}
	if ok < 40 || bad < 20 || plain < 20 || reordered == 0 {
		t.Fatalf("ok %d, error %d, plain %d, key order changed by coercion %d", ok, bad, plain, reordered)
	}
}

func keyOrder(s string) string {
	var keys []string
	dec := json.NewDecoder(strings.NewReader(s))
	if tok, _ := dec.Token(); tok != json.Delim('{') {
		return ""
	}
	for dec.More() {
		k, _ := dec.Token()
		keys = append(keys, k.(string))
		var skip json.RawMessage
		_ = dec.Decode(&skip)
	}
	return strings.Join(keys, ",")
}

// ---- uuid: a Go port of pi-ai's uuidv7 (packages/ai/src/utils/uuid.ts) reproduces the vectors.

type uuidGen struct {
	lastOrdinary int64
	sequence     *big.Int
}

func newUUID(now func() int64, random func([]byte)) func(*int64) string {
	g := &uuidGen{lastOrdinary: -1}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 41), big.NewInt(1))
	return func(ts *int64) string {
		effective := now()
		if ts != nil {
			effective = *ts
		} else if g.lastOrdinary > effective {
			effective = g.lastOrdinary
		}
		if ts == nil {
			g.lastOrdinary = effective
		}
		b := make([]byte, 16)
		random(b)
		if g.sequence == nil {
			g.sequence = big.NewInt(int64(b[1])<<32 | int64(b[2])<<24 | int64(b[3])<<16 | int64(b[4])<<8 | int64(b[5]))
		} else {
			if g.sequence.Cmp(max) == 0 {
				panic("UUIDv7 generator sequence exhausted")
			}
			g.sequence.Add(g.sequence, big.NewInt(1))
		}
		seq := g.sequence.Uint64()
		var t8 [8]byte
		binary.BigEndian.PutUint64(t8[:], uint64(effective))
		copy(b[0:6], t8[2:8])
		b[6] = 0x70 | byte((seq>>37)&0x0f)
		b[7] = byte((seq >> 29) & 0xff)
		b[8] = 0x80 | byte((seq>>23)&0x3f)
		b[9] = byte((seq >> 15) & 0xff)
		b[10] = byte((seq >> 7) & 0xff)
		b[11] = byte((seq&0x7f)<<1) | (b[11] & 0x01)
		h := fmt.Sprintf("%x", b)
		return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	}
}

func TestUUIDVectorsAreReproducedByAPortOfPiAI(t *testing.T) { CheckUUID(t, newUUID) }

func TestAUUIDGeneratorWithoutTheSequenceCarryFailsTheVectors(t *testing.T) {
	var f failures
	CheckUUID(&f, func(now func() int64, random func([]byte)) func(*int64) string {
		inner := newUUID(now, random)
		// A fresh generator per call forgets the process-wide sequence and the monotonic timestamp.
		return func(ts *int64) string { return newUUID(now, random)(ts) + "" + inner(nil)[:0] }
	})
	if f.n == 0 {
		t.Fatal("the uuid vectors do not detect a generator without process-level state")
	}
}

var _ = bytes.Equal

func TestReferenceEncodeReproducesEveryEncodeVector(t *testing.T) { CheckEncode(t, ReferenceEncode) }
