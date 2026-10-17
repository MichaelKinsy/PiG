package jv

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// errNotObject reports JSON text that Parse accepts but whose top-level value is not an object.
var errNotObject = errors.New("JSON is not an object")

// splitArray is a test oracle for SplitArrayMembers: the raw text of each element of the array text, with SplitArrayMembers'
// grammar, errors and depth bound, read by skipValue alone.
func splitArray(text []byte) ([][]byte, error) {
	p := parser{s: text}
	p.ws()
	if p.i >= len(p.s) || p.s[p.i] != '[' {
		return nil, p.otherThan(ErrNotArray)
	}
	p.i++
	p.ws()
	var out [][]byte
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		out = [][]byte{}
	} else {
		out = make([][]byte, 0, firstMembers)
	elements:
		for {
			p.ws()
			start := p.i
			if err := p.skipValue(1); err != nil {
				return nil, err
			}
			out = append(out, p.s[start:p.i])
			p.ws()
			if p.i >= len(p.s) {
				return nil, ErrSyntax
			}
			switch p.s[p.i] {
			case ',':
				p.i++
			case ']':
				p.i++
				break elements
			default:
				return nil, ErrSyntax
			}
		}
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, ErrSyntax
	}
	return out, nil
}

// splitObject is a test oracle for SplitArrayMembers and MemberText: the members of the object text in the order they are
// written, a repeated key reported each time; a text that Parse accepts as another kind of value fails with errNotObject.
func splitObject(text []byte) ([]Member, error) {
	p := parser{s: text}
	p.ws()
	if p.i >= len(p.s) || p.s[p.i] != '{' {
		return nil, p.otherThan(errNotObject)
	}
	out, err := p.members(0)
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, ErrSyntax
	}
	return out, nil
}

// checkSplit holds the contract of splitArray and splitObject against Parse for one text: they accept exactly what Parse accepts,
// report the top-level value's kind, and hand back, for each element or member, text that Parse reads as that element or member.
func checkSplit(t testing.TB, text string) {
	t.Helper()
	want, perr := ParseString(text)
	elems, aerr := splitArray([]byte(text))
	members, oerr := splitObject([]byte(text))
	rows, rerr := SplitArrayMembers([]byte(text))
	if perr != nil {
		if !errors.Is(aerr, ErrSyntax) || !errors.Is(oerr, ErrSyntax) || !errors.Is(rerr, ErrSyntax) {
			t.Fatalf("%q: Parse rejects (%v) but splitArray gives %v, splitObject %v and SplitArrayMembers %v", short(text), perr, aerr, oerr, rerr)
		}
		return
	}
	if (aerr == nil) != (rerr == nil) || !errors.Is(rerr, aerr) {
		t.Fatalf("%q: splitArray gives %v and SplitArrayMembers %v", short(text), aerr, rerr)
	}
	if aerr == nil {
		checkRows(t, text, elems, rows)
	}
	switch w := want.(type) {
	case []any:
		if aerr != nil || !errors.Is(oerr, errNotObject) || len(elems) != len(w) {
			t.Fatalf("%q: an array of %d: splitArray %d elements, err %v; splitObject err %v", short(text), len(w), len(elems), aerr, oerr)
		}
		for i, raw := range elems {
			got, err := Parse(raw)
			if err != nil || !Equal(got, w[i]) {
				t.Fatalf("%q: element %d %q parses to %v (%v), want %v", short(text), i, raw, got, err, w[i])
			}
		}
	case *Object:
		if oerr != nil || !errors.Is(aerr, ErrNotArray) {
			t.Fatalf("%q: an object: splitObject err %v; splitArray err %v", short(text), oerr, aerr)
		}
		checkMemberText(t, text, members)
		o := NewObject()
		for _, m := range members {
			v, err := Parse(m.Raw)
			if err != nil {
				t.Fatalf("%q: member %q has text %q that does not parse: %v", short(text), m.Key, m.Raw, err)
			}
			o.Set(m.Key, v)
		}
		if !Equal(o, w) || Stringify(o) != Stringify(w) {
			t.Fatalf("%q: members rebuild %s, want %s", short(text), Stringify(o), Stringify(w))
		}
	default:
		if !errors.Is(aerr, ErrNotArray) || !errors.Is(oerr, errNotObject) {
			t.Fatalf("%q: a %T: splitArray err %v; splitObject err %v", short(text), want, aerr, oerr)
		}
	}
}

// checkRows holds SplitArrayMembers to splitArray and splitObject: row i is nil for an element that is not an object, and else the
// members splitObject gives for it.
func checkRows(t testing.TB, text string, elems [][]byte, rows [][]Member) {
	t.Helper()
	if len(rows) != len(elems) {
		t.Fatalf("%q: %d elements, %d rows", short(text), len(elems), len(rows))
	}
	for i, elem := range elems {
		want, err := splitObject(elem)
		if err != nil {
			if rows[i] != nil {
				t.Fatalf("%q: element %d %q is not an object but has members %v", short(text), i, elem, rows[i])
			}
			continue
		}
		if rows[i] == nil || len(rows[i]) != len(want) {
			t.Fatalf("%q: element %d %q: %v, want %v", short(text), i, elem, rows[i], want)
		}
		for j := range want {
			if rows[i][j].Key != want[j].Key || !bytes.Equal(rows[i][j].Raw, want[j].Raw) {
				t.Fatalf("%q: element %d member %d: %v, want %v", short(text), i, j, rows[i][j], want[j])
			}
		}
		checkMemberText(t, string(elem), want)
	}
}

// checkMemberText holds MemberText to the members splitObject gave: the last member of a name, nil for a name that is not there.
func checkMemberText(t testing.TB, text string, members []Member) {
	t.Helper()
	last := map[string][]byte{"zz-absent": nil}
	for _, m := range members {
		last[m.Key] = m.Raw
	}
	for key, want := range last {
		if got := MemberText([]byte(text), key); !bytes.Equal(got, want) || (got == nil) != (want == nil) {
			t.Fatalf("%q: MemberText %q = %q, want %q", short(text), key, got, want)
		}
	}
}

// short is text cut to a length a failure message can show.
func short(text string) string {
	if len(text) > 120 {
		return text[:60] + "..." + text[len(text)-60:]
	}
	return text
}

func scanCorpus(t testing.TB) []string {
	f, err := os.Open("testdata/corpus.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	var out []string
	for sc.Scan() {
		var c struct {
			In string `json:"in"`
		}
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c.In)
	}
	return out
}

// TestSplitAgreesWithParseOnTheNodeCorpus replays the corpus that testdata/gen.mjs wrote from Node's JSON.parse (accepted and
// rejected texts) through the split functions.
func TestSplitAgreesWithParseOnTheNodeCorpus(t *testing.T) {
	corpus := scanCorpus(t)
	for _, in := range corpus {
		checkSplit(t, in)
		checkSplit(t, "["+in+","+in+"]")
		checkSplit(t, `{"k":`+in+`,"k":`+in+`}`)
	}
	if len(corpus) < 1500 {
		t.Fatalf("corpus too small: %d lines", len(corpus))
	}
}

// TestSplitAgreesWithParseOnEveryMutation damages each short corpus text one byte at a time (deleted, or replaced by a character
// the grammar treats specially): a mutation that Parse rejects the split functions reject, and one that it accepts they accept.
func TestSplitAgreesWithParseOnEveryMutation(t *testing.T) {
	repl := []byte{'"', '\\', ',', ':', '{', '}', '[', ']', '0', 'u', 'e', '-', ' ', 0, 0x1f, 0x80, 0xed}
	n := 0
	for _, in := range scanCorpus(t) {
		if len(in) > 160 {
			continue
		}
		b := []byte(in)
		for i := range b {
			checkSplit(t, string(append(append([]byte{}, b[:i]...), b[i+1:]...)))
			for _, r := range repl {
				m := append([]byte{}, b...)
				m[i] = r
				checkSplit(t, string(m))
				n++
			}
		}
		for _, wrapped := range []string{"[" + in + "]", `{"a":` + in + `}`} {
			checkSplit(t, wrapped)
			checkSplit(t, wrapped[:len(wrapped)-1])
			checkSplit(t, wrapped+"x")
		}
	}
	if n < 10000 {
		t.Fatalf("only %d mutations ran", n)
	}
}

func TestSplitShapes(t *testing.T) {
	for _, in := range []string{
		``, ` `, `[`, `]`, `[]`, ` [ ] `, `[1,]`, `[,1]`, `[1 2]`, `{}`, ` { } `, `{"a"}`, `{"a":}`, `{"a":1,}`, `{,}`, `{"a":1 "b":2}`,
		`{'a':1}`, `{1:2}`, `[1] x`, `[1]]`, `{"a":1}}`, `null`, `true`, `1`, `"[]"`, `-`, `01`, `1.`, `.5`, `1e`, `1e+`, `-01`, `tru`, `nul`,
		`[true,false,null,"x",1,-0,1e5,1E-5,0.5]`, `{"a":{"b":[{"c":[]}]},"a":2,"\u0061":3}`, "\"\\ud800\"", "[\"\\ud83d\\ude00\",\"\\udc00\"]",
		"[\"\xed\xa0\x80\"]", "[\"\xff\xfe\"]", "[\"\n\"]", "[\"\\x\"]", "[\"\\u12\"]", "[\"\\u12G4\"]", `["\`, `["\"`, `[` + string(rune(0x2028)) + `]`,
		`{"a":1}` + "\n", "\t[1]\r\n", "\v[1]", `[1,2,3`, `{"a":1`, `{"a"`, `{"a":`, `{"`, `["`, `[1e999,-1e999]`, `1e999999999999999999`,
	} {
		checkSplit(t, in)
	}
}

// TestSplitDepthBoundIsParses nests arrays to the bound Parse has and one level past it.
func TestSplitDepthBoundIsParses(t *testing.T) {
	for _, depth := range []int{maxDepth - 1, maxDepth, maxDepth + 1, maxDepth + 2} {
		open, shut := make([]byte, depth), make([]byte, depth)
		for i := range open {
			open[i], shut[i] = '[', ']'
		}
		checkSplit(t, string(open)+string(shut))
		checkSplit(t, `{"a":`+string(open)+string(shut)+`}`)
		// An object element: SplitArrayMembers reads it with members, not skipValue, and must count its depth as Parse does.
		checkSplit(t, `[{"a":`+string(open)+string(shut)+`}]`)
	}
}

func TestSplitReturnsTheRawTextOfEachValue(t *testing.T) {
	text := []byte(` [ {"a" : 1 } , "x\u0041" ,[ ] , 1.50 ] `)
	elems, err := splitArray(text)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{`{"a" : 1 }`, `"x\u0041"`, `[ ]`, `1.50`} {
		if string(elems[i]) != want {
			t.Errorf("element %d = %q, want %q", i, elems[i], want)
		}
	}
	members, err := splitObject([]byte(`{ "t\u0079pe" : "done" , "m":{"x":[1, 2]}, "type":1 }`))
	if err != nil || len(members) != 3 {
		t.Fatalf("members %v, err %v", members, err)
	}
	for i, want := range []Member{{"type", []byte(`"done"`)}, {"m", []byte(`{"x":[1, 2]}`)}, {"type", []byte(`1`)}} {
		if members[i].Key != want.Key || !bytes.Equal(members[i].Raw, want.Raw) {
			t.Errorf("member %d = %q: %q, want %q: %q", i, members[i].Key, members[i].Raw, want.Key, want.Raw)
		}
	}
}

func TestMemberTextDoesNotPanicOnUnvalidatedText(t *testing.T) {
	for _, in := range scanCorpus(t) {
		for i := 0; i <= len(in); i += 7 {
			_ = MemberText([]byte(in[:i]), "a")
			_ = MemberText([]byte("{"+in[:i]), "a")
			_ = MemberText([]byte(`{"a":`+in[:i]), "a")
		}
	}
}

func TestIsNonEmptyArray(t *testing.T) {
	for in, want := range map[string]bool{`[]`: false, `[ ]`: false, "[\n]": false, `[1]`: true, `[ null ]`: true, `[[]]`: true, ``: false, `{}`: false, `"["`: false, `1`: false} {
		if got := IsNonEmptyArray([]byte(in)); got != want {
			t.Errorf("IsNonEmptyArray(%q) = %v, want %v", in, got, want)
		}
	}
}

func FuzzSplitAgreesWithParse(f *testing.F) {
	for _, s := range []string{`{"a":[1,2,{"b":"\ud800"}]}`, `[{"type":"done","message":{}},{"type":"x"}]`, "\"\xed\xa3\xb3\"", `{"a":1,"a":2}`, `[1,]`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { checkSplit(t, s) })
}
