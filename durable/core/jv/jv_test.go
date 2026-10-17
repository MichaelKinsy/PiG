package jv

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
)

// TestParseStringifyMatchesNode replays testdata/corpus.jsonl, which
// testdata/gen.mjs wrote from Node's JSON.parse and JSON.stringify.
func TestParseStringifyMatchesNode(t *testing.T) {
	f, err := os.Open("testdata/corpus.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	n, rejected := 0, 0
	for sc.Scan() {
		var c struct {
			In  string  `json:"in"`
			Out *string `json:"out"`
		}
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		n++
		v, err := ParseString(c.In)
		if c.Out == nil {
			rejected++
			if err == nil {
				t.Errorf("parse(%q) succeeded, Node rejects", c.In)
			}
			continue
		}
		if err != nil {
			t.Errorf("parse(%q): %v, Node accepts", c.In, err)
			continue
		}
		if got := Stringify(v); got != *c.Out {
			t.Errorf("stringify(parse(%q)):\n got %s\nwant %s", c.In, got, *c.Out)
		}
	}
	if n < 1500 || rejected == 0 {
		t.Fatalf("corpus too small: %d lines, %d rejected", n, rejected)
	}
}

func TestFormatNumber(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		want string
	}{
		{0, "0"}, {math.Copysign(0, -1), "0"}, {1.5, "1.5"}, {1e21, "1e+21"}, {1e20, "100000000000000000000"}, {1e-7, "1e-7"},
		{1e-6, "0.000001"}, {123456789012345680000, "123456789012345680000"}, {0.30000000000000004, "0.30000000000000004"},
		{-1e-7, "-1e-7"}, {5e-324, "5e-324"}, {math.MaxFloat64, "1.7976931348623157e+308"}, {1700000000000.25, "1700000000000.25"},
		{100, "100"}, {12345.678e10, "123456780000000"}, {9007199254740992, "9007199254740992"}, {1.5e300, "1.5e+300"},
	} {
		if got := FormatNumber(tc.v); got != tc.want {
			t.Errorf("FormatNumber(%v) = %s, want %s", tc.v, got, tc.want)
		}
	}
}

func TestObjectOrder(t *testing.T) {
	o := NewObject()
	for _, k := range []string{"b", "2", "a", "10", "1", "01"} {
		o.Set(k, 1.0)
	}
	if got := Stringify(o); got != `{"1":1,"2":1,"10":1,"b":1,"a":1,"01":1}` {
		t.Fatal(got)
	}
	o.Set("b", 2.0)
	o.Delete("2")
	if got := Stringify(o); got != `{"1":1,"10":1,"b":2,"a":1,"01":1}` {
		t.Fatal(got)
	}
}

func TestUTF16(t *testing.T) {
	lone, _ := ParseString(`"\ud83d"`)
	low, _ := ParseString(`"\ude00"`)
	joined := Concat(lone.(string), low.(string))
	if Stringify(joined) != `"😀"` {
		t.Fatalf("Concat: %s", Stringify(joined))
	}
	if UTF16Len(joined) != 2 || UTF16Len("a😀é") != 4 {
		t.Fatal("UTF16Len")
	}
	cut := SliceFrom("😀x", 1)
	if Stringify(cut) != `"\ude00x"` {
		t.Fatalf("SliceFrom: %s", Stringify(cut))
	}
	if !HasPrefix("😀x", lone.(string)) || HasPrefix("😀x", low.(string)) {
		t.Fatal("HasPrefix")
	}
}

func TestEqualCloneIgnoreKeyOrder(t *testing.T) {
	a, _ := ParseString(`{"a":[1,{"b":2,"c":3}],"d":null}`)
	b, _ := ParseString(`{"d":null,"a":[1,{"c":3,"b":2}]}`)
	if !Equal(a, b) || Stringify(a) == Stringify(b) {
		t.Fatal("Equal must ignore order")
	}
	c := Clone(a)
	if !Equal(a, c) || Stringify(a) != Stringify(c) {
		t.Fatal("Clone")
	}
	c.(*Object).Set("d", 1.0)
	if Equal(a, c) {
		t.Fatal("Clone must be deep and independent")
	}
}

func FuzzParseNeverPanics(f *testing.F) {
	f.Add(`{"a":[1,2,{"b":"\ud800"}]}`)
	// Raw CESU-8 surrogate bytes once parsed as two lone surrogates and stringified as an escaped pair.
	f.Add("\"\xed\xa3\xb3\xed\xb3\xb3\"")
	f.Fuzz(func(t *testing.T, s string) {
		v, err := ParseString(s)
		if err != nil {
			return
		}
		out := Stringify(v)
		v2, err := ParseString(out)
		if err != nil || Stringify(v2) != out {
			t.Fatalf("stringify output %s does not reparse to itself", out)
		}
	})
}

// An object built member by member holds its first four members in the block it is allocated in: one allocation for the object
// and both slices, where growing from nothing took three and the object itself a fourth. A fifth member grows the slices.
func TestAnObjectOfFourMembersBuiltByHandAllocatesOnce(t *testing.T) {
	var sink *Object
	allocs := testing.AllocsPerRun(100, func() {
		o := NewObject()
		o.Set("a", 1.0)
		o.Set("b", 2.0)
		o.Set("c", 3.0)
		o.Set("d", 4.0)
		sink = o
	})
	if allocs != 1 {
		t.Fatalf("allocations = %v", allocs)
	}
	grown := NewObjectCap(2)
	for i, k := range []string{"a", "b", "c", "d", "e", "f"} {
		grown.Set(k, float64(i))
	}
	empty, err := ParseString("{}")
	if err != nil {
		t.Fatal(err)
	}
	empty.(*Object).Set("x", true)
	if got := Stringify(grown) + Stringify(empty); got != `{"a":0,"b":1,"c":2,"d":3,"e":4,"f":5}{"x":true}` {
		t.Fatalf("members = %s", got)
	}
	if sink.Len() != 4 {
		t.Fatalf("members = %d", sink.Len())
	}
}

// The integer fast paths (smallInteger on parse, the digits loop of AppendNumber) give what strconv gives: every integer around the
// digit-count and magnitude limits, both signs, and the tokens that must stay with ParseFloat ("-0", fractions, exponents). The
// magnitudes reach 2^62: above 2^54 the exact digits of an integer double and its shortest digits differ (2^54+8 is
// 18014398509481992, Number::toString gives 18014398509481990), so a fast path that let such a double through prints wrong digits;
// and a token of 19 or more digits overflows the int64 the parse path accumulates in.
func TestIntegerFastPathsAgreeWithStrconv(t *testing.T) {
	values := []int64{0, 1, 9, 10, 99, 100, 12345, 999999999999999, 100000000000000, 1e15 - 1, 1e15, 1e15 + 1, 1<<53 - 1, 1 << 53, 1<<53 + 1,
		1<<54 + 8, 1e16 + 8, 1e17 + 8, 1e18 + 8, math.MaxInt64}
	for r := uint64(1); len(values) < 4000; r = r*6364136223846793005 + 1442695040888963407 {
		values = append(values, int64(r>>2)%(1<<((r&63)%62+1)))
	}
	for _, v := range values {
		for _, n := range []int64{v, -v} {
			text := strconv.FormatInt(n, 10)
			got, err := ParseString(text)
			want, _ := strconv.ParseFloat(text, 64)
			if err != nil || got.(float64) != want || (n == 0 && math.Signbit(got.(float64))) {
				t.Fatalf("parse %s = %v (%v), strconv %v", text, got, err, want)
			}
			f := float64(n)
			ref := strconv.FormatFloat(f, 'f', -1, 64)
			if n == 0 {
				ref = "0"
			}
			if f < 1e21 && f > -1e21 {
				if out := string(AppendNumber(nil, f)); out != ref {
					t.Fatalf("format %v = %s, want %s", f, out, ref)
				}
			}
		}
	}
	negZero, err := ParseString("-0")
	if err != nil || !math.Signbit(negZero.(float64)) || negZero.(float64) != 0 {
		t.Fatalf("-0 parses to %v (%v)", negZero, err)
	}
	for _, text := range []string{"1.5", "1e3", "1E+3", "-2.5e-3", "1234567890123456", "0.1", "01", "9223372036854775807", "9223372036854775808",
		"-9223372036854775809", "18446744073709551616", "99999999999999999999", "123456789012345678901234567890"} {
		got, err := ParseString(text)
		want, perr := strconv.ParseFloat(text, 64)
		if text == "01" {
			if err == nil {
				t.Fatal("01 parses")
			}
			continue
		}
		if err != nil || perr != nil || got.(float64) != want {
			t.Fatalf("parse %s = %v (%v), strconv %v", text, got, err, want)
		}
	}
	for f, want := range map[float64]string{1.5: "1.5", 1e15: "1000000000000000", 123456789012345678: "123456789012345680", 1e21: "1e+21", 1<<54 + 8: "18014398509481990", 1 << 60: "1152921504606847000", 1e-7: "1e-7", 0.000001: "0.000001", -0.5: "-0.5", -1e15: "-1000000000000000"} {
		if out := string(AppendNumber(nil, f)); out != want {
			t.Fatalf("format %v = %s, want %s", f, out, want)
		}
	}
}

// find compares a member's length before its bytes; keys of one length, the empty key and keys one byte apart each stay their own
// member, below and above the size from which an object keeps an index.
func TestObjectFindSeparatesKeysOfOneLength(t *testing.T) {
	for _, members := range []int{3, indexThreshold + 4} {
		o := NewObject()
		keys := []string{"", "a", "b", "id", "ie", "to", "type", "tyqe", "name"}
		for i := range members - len(keys) {
			keys = append(keys, "pad"+strconv.Itoa(i))
		}
		for i, k := range keys[:members] {
			o.Set(k, float64(i))
		}
		for i, k := range keys[:members] {
			if v, ok := o.Get(k); !ok || v != float64(i) {
				t.Fatalf("%d members: Get(%q) = %v, %v, want %d", members, k, v, ok, i)
			}
		}
		for _, absent := range []string{"c", "ic", "tz", "typf", "nam", "names", "zzzzzzzz"} {
			if o.Has(absent) {
				t.Fatalf("%d members: Has(%q) is true", members, absent)
			}
		}
	}
}
