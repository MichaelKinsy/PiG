package jsstring

import (
	"bytes"
	"math"
	"math/rand"
	"os/exec"
	"testing"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// JSON.stringify writes <, >, & and U+2028/U+2029 raw and escapes only quotes, backslashes and C0 controls; an escaped backslash before literal "u2028" text
// is not a line terminator.
func TestMarshalJSONMatchesJSONStringify(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{"a<b>&c", `"a<b>&c"`},
		{"x\u2028y\u2029z", "\"x\u2028y\u2029z\""},
		{`\u2028`, `"\\u2028"`},
		{"\x1b[0m\x7f", `"\u001b[0m` + "\x7f" + `"`},
		{map[string]any{"k": "<&>"}, `{"k":"<&>"}`},
		{math.Copysign(0, -1), `0`},
		{rawJSON(`{"a":-0,"b":[-0.0,-0e5,-1e-400,-1,-0.5],"-0":"-0 x\"-0","c":-0}`), `{"a":0,"b":[0,0,0,-1,-0.5],"-0":"-0 x\"-0","c":0}`},
	} {
		got, err := MarshalJSON(tc.in)
		if err != nil || string(got) != tc.want {
			t.Errorf("MarshalJSON(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

// AppendString writes what MarshalJSON writes for a string, except that ill-formed UTF-8 is the character U+FFFD, as JSON.stringify writes the
// character a decoded JavaScript string holds, not the escape encoding/json writes.
func TestAppendStringIsJSONStringify(t *testing.T) {
	for _, s := range []string{"", "plain ascii 0-9 ~", "<&>", "quote\"", `back\slash`, "tab\t", "\x00\x1f\x7f", "é", "\u2028\u2029", "\xed\xa0\x80", "😀", "\xed\xa0\xbd\xed\xb8\x80"} {
		want, err := MarshalJSON(s)
		if err != nil {
			t.Fatal(err)
		}
		if got := AppendString([]byte("x"), s); string(got) != "x"+string(want) {
			t.Errorf("AppendString(%q) = %s, want x%s", s, got, want)
		}
	}
	for s, want := range map[string]string{"\xff": "\"\ufffd\"", "a\xf0\x9f\x98b": "\"a\ufffdb\""} {
		if got := string(AppendString(nil, s)); got != want {
			t.Errorf("AppendString(%q) = %q, want %q", s, got, want)
		}
	}
}

// rawJSON is a custom marshaler that emits its text unchanged, as a record's own MarshalJSON does through plain json.Marshal.
type rawJSON string

func (r rawJSON) MarshalJSON() ([]byte, error) { return []byte(r), nil }

var _ json.Marshaler = rawJSON("")

// JSON.stringify enumerates the array-index keys of an object first, in ascending numeric order; a Go map has no insertion order, so the other keys follow in byte order. Pinned against Node: each key set is inserted into a JavaScript object in byte order and stringified.
func TestMarshalJSONOrdersArrayIndexKeysLikeJSONStringify(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pool := []string{"0", "1", "2", "9", "10", "11", "100", "01", "00", "-1", "+1", "1.5", "1e3", "4294967294", "4294967295", "4294967296", "18446744073709551616", "a", "b", "B", "é", "", " 1", "1 ", "٣"}
	var sets [][]string
	for range 400 {
		var keys []string
		for _, key := range pool {
			if rng.Intn(3) == 0 {
				keys = append(keys, key)
			}
		}
		sets = append(sets, keys)
	}
	input, err := json.Marshal(sets)
	if err != nil {
		t.Fatal(err)
	}
	const script = `let s="";process.stdin.setEncoding("utf8");process.stdin.on("data",c=>s+=c).on("end",()=>{const out=JSON.parse(s).map(keys=>{const o={};for(const k of [...keys].sort())o[k]=1;return JSON.stringify(o)});process.stdout.write(JSON.stringify(out))})`
	cmd := exec.CommandContext(t.Context(), "node", "-e", script)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, keys := range sets {
		object := map[string]any{}
		for _, key := range keys {
			object[key] = 1
		}
		got, err := MarshalJSON(object)
		if err != nil || string(got) != want[i] {
			t.Errorf("MarshalJSON(keys %q) = %q, %v; want %q", keys, got, err, want[i])
		}
	}
}
