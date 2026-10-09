package jv

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
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
