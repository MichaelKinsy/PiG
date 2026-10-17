package jsonstringify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"
)

// fastPathEdges are the texts each scan rule exists for, each in a scalar, array, object and repeated position.
func fastPathEdges() []string {
	var out []string
	nums := []string{"0", "-0", "-0.0", "0.0", "1", "-1", "5", "1.5", "-1.5", "0.5", "-0.5", "0.000001", "-0.000001", "0.0000015", "0.0000001", "0.00001", "123456789012345", "-123456789012345", "1234567890123456", "999999999999999", "100000000000000", "1000000000000000", "999999999999999.9", "99999999999999.9", "0.123456789012345", "0.1234567890123456", "1.23456789012345", "12345678.9012345", "0.30000000000000004", "0.3", "9007199254740991", "9007199254740993", "100000000000000000000", "1000000000000000000000", "123456789012345.5", "1.7976931348623157", "4.9", "0.000009999999999", "0.0000099999999999999", "0.00000999999999999999", "2.5e-7", "1e-6", "1e21", "1E2", "10", "1.25", "0.1", "0.2", "0.7", "3.14159", "2.718281828459045", "2.71828182845905"}
	keys := []string{"0", "1", "9", "10", "01", "00", "-0", "-1", "4294967294", "4294967295", "4294967296", "12345678901", "1.0", "1e3", " 1", "1 ", "a", "", "__proto__", "constructor", "toString", "length", "\u2028", "\u2029", "é", "😀", "\\\"", "\\\\", "\\n", "\\t", "\\r", "\\/", "\\b", "\\f", "\\u0030", "\\ud800", "\\udc00", "\\ud83d\\ude00", "a\\u0000b"}
	strs := []string{"", "\u2028", "\u2029", "a\u2028b\u2029c", "\u007f", "\u0080", "\ufeff", "\uffff", "😀", "\\ud800", "\\udfff", "\\ud800\\udc00", "\\udbff\\udfff", "\\ud800x", "x\\udc00", "\\ud83d\\ude00", "\\u2028", "\\u2029", "\\u007f", "\\u0000", "\\u001f", "\\u0020", "\\u00e9", "\\n", "\\r\\n", "\\t", "\\\"", "\\\\", "\\/", "\\b", "\\f", "\\u000a", "\\u0022", "\\u005c", "ab\\u0063"}
	for _, n := range nums {
		out = append(out, n, "["+n+"]", `{"k":`+n+`}`, "["+n+","+n+"]")
	}
	for _, k := range keys {
		out = append(out, `{"`+k+`":1}`, `{"a":1,"`+k+`":2}`, `{"`+k+`":1,"`+k+`":2}`, `{"b":{"`+k+`":[1]},"`+k+`":true}`)
	}
	for _, s := range strs {
		out = append(out, `"`+s+`"`, `["`+s+`"]`, `{"`+s+`":"`+s+`"}`)
	}
	out = append(out, `{"a":1,"b":{"a":2},"a":3}`, `{"a":{"b":1},"b":2}`, `{"a":{"b":1,"b":2}}`, `[[[[]]]]`, `{}`, `[]`, `[{}]`, `{"":{"":{}}}`, `true`, `false`, `null`, `[true,false,null]`)
	deep := strings.Repeat("[", 40) + strings.Repeat("]", 40)
	out = append(out, deep, strings.Repeat(`{"a":`, 33)+"1"+strings.Repeat("}", 33))
	var many []string
	for i := range 30 {
		many = append(many, `"k`+string(rune('a'+i%26))+string(rune('a'+i/26))+`":1`)
	}
	out = append(out, "{"+strings.Join(many, ",")+"}", "{"+strings.Join(many[:24], ",")+"}", "{"+strings.Join(many[:25], ",")+"}")
	// Objects past the linear repeat check: repeated keys early, late and across the boundary, an integer-like key late, and nesting.
	var large []string
	for i := range 150 {
		large = append(large, fmt.Sprintf(`"key%d":%d`, i, i))
	}
	big := "{" + strings.Join(large, ",") + "}"
	out = append(out, big, "["+big+","+big+"]", `{"a":`+big+`,"b":`+big+"}",
		"{"+strings.Join(large, ",")+`,"key3":0}`, "{"+strings.Join(large, ",")+`,"key149":0}`, "{"+strings.Join(large[:24], ",")+`,"key0":0}`,
		"{"+strings.Join(large[:30], ",")+`,"key29":0}`, "{"+strings.Join(large, ",")+`,"77":0}`, "{"+strings.Join(large, ",")+`,"k":"\u2028\\u001b"}`)
	return out
}

// The scan against Node itself, not only against the rewrite: every text the scan accepts is JSON.stringify(JSON.parse(text)), across the shapes the rules exist for (-0 and the other number forms around 15 digits and 1e-6, raw U+2028/U+2029, integer-like and repeated keys, escaped lone surrogates), and Canonicalize equals Node on all of them.
func TestFastPathMatchesNodeJSON(t *testing.T) {
	texts := fastPathEdges()
	texts = append(texts, fastPathCorpus(rand.New(rand.NewSource(77)), 60000)...)
	var valid []string
	for _, text := range texts {
		if utf8.ValidString(text) {
			valid = append(valid, text)
		} else if IsCanonical(bytes.TrimSpace([]byte(text))) {
			t.Errorf("scan accepted invalid UTF-8 %q", text)
		}
	}
	in, _ := json.Marshal(valid)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/stringify_or_error.mjs")
	cmd.Stdin = bytes.NewReader(in)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []*string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	accepted, compared, bad := 0, 0, 0
	for i, text := range valid {
		trimmed := bytes.TrimSpace([]byte(text))
		fast := IsCanonical(trimmed)
		if want[i] == nil {
			// Canonicalize's precondition is validated JSON; bytes.TrimSpace removes \f, \v and Unicode spaces JSON.parse rejects, on both paths alike.
			continue
		}
		compared++
		if fast {
			accepted++
			if string(trimmed) != *want[i] {
				t.Errorf("scan accepted %q, JSON.stringify gives %q", trimmed, *want[i])
				bad++
			}
		}
		got, err := Canonicalize([]byte(text))
		if err != nil || string(got) != *want[i] {
			if bad < 20 {
				t.Errorf("Canonicalize(%q) = %q %v, node %q (fast %v)", text, got, err, *want[i], fast)
			}
			bad++
		}
	}
	t.Logf("%d texts, %d node-valid, %d accepted by the scan", len(valid), compared, accepted)
	if accepted < 5000 {
		t.Errorf("only %d texts took the scan", accepted)
	}
}

// Keys past the scanner's inline stack spill to the heap: objects nested past maxInlineKeys keys in all are still recognized, and a repeated
// key at the deepest level is still refused.
func TestFastPathKeyStackBound(t *testing.T) {
	var keys []string
	for i := range maxLinearObjectKeys - 1 {
		keys = append(keys, fmt.Sprintf(`"k%d":%d`, i, i))
	}
	members := strings.Join(keys, ",")
	text := "1"
	repeated := `{"k1":1,"next":1,"k1":2}`
	for range maxInlineKeys/(maxLinearObjectKeys-1) + 2 {
		text = "{" + members + `,"next":` + text + "}"
		repeated = "{" + members + `,"next":` + repeated + "}"
	}
	got, err := Canonicalize([]byte(text))
	if err != nil || string(got) != text {
		t.Fatalf("Canonicalize changed %d bytes of nested objects (%v)", len(text), err)
	}
	if !IsCanonical([]byte(text)) {
		t.Fatalf("scan refused %d nested keys, past its %d-key inline stack", (maxInlineKeys/(maxLinearObjectKeys-1)+2)*maxLinearObjectKeys, maxInlineKeys)
	}
	if IsCanonical([]byte(repeated)) {
		t.Fatal("scan accepted a repeated key below the inline stack")
	}
}
