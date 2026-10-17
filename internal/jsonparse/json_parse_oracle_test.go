package jsonparse

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// pi: JSON.parse is what Pi calls on provider streams, session files and settings; V8's SyntaxError message is shown to users.
// Random corruptions of valid documents give the same message under Validate as under the Node that runs the pinned Pi.
func TestValidateMatchesNodeJSONParse(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	docs := []string{
		`{"a":1,"b":[true,false,null,"x\u00e9\n"],"c":{"d":-1.5e+3,"e":""}}`, `[1,2,3]`, `"text"`, `12`, `{}`, `[]`, `{"k":"v"}`,
		`{"type":"message","id":"abc","message":{"role":"user","content":[{"type":"text","text":"hello 日本語 😀"}]}}`, "{\n  \"pretty\": [\n    1,\n    2\n  ]\n}",
	}
	pieces := []string{"{", "}", "[", "]", ",", ":", "\"", "'", "\\", "\\u12", "\\x", "\t", "\n", "\r", " ", "\x00", "\x1f", "\u00a0", "\ufeff", "\u2028", "é", "日", "😀", "\\ud800", "\\udc00x", "tru", "nul", "fals", "-", "+", ".", "e", "E", "0", "01", "1.", ".5", "NaN", "Infinity", "undefined", "/*c*/", "//c", "]]", "}}", ",,", "::", "\"a\"", "1e", "-0", "0x1", strings.Repeat("9", 30)}
	// The two V8 rules fixed here, at each position: a number or string after a broken literal, and an escape of a code unit above U+00FF.
	inputs := []string{`t1`, `[tru1]`, `{"a":nul"x"}`, `fals"`, `[f-1]`, `n"`, `"\日"`, `"a\😀"`, `["\é"]`, `"\ÿ"`, `"\Ā"`}
	for len(inputs) < 4011 {
		doc := []rune(docs[rng.Intn(len(docs))])
		for range 1 + rng.Intn(3) {
			at := rng.Intn(len(doc) + 1)
			switch rng.Intn(4) {
			case 0:
				if at < len(doc) {
					doc = append(doc[:at], doc[at+1:]...)
				}
			case 1:
				doc = append(doc[:at], append([]rune(pieces[rng.Intn(len(pieces))]), doc[at:]...)...)
			case 2:
				doc = doc[:at]
			default:
				if at < len(doc) {
					doc[at], _ = utf8.DecodeRuneInString(pieces[rng.Intn(len(pieces))])
				}
			}
		}
		if s := string(doc); strings.ToValidUTF8(s, "") == s {
			inputs = append(inputs, s)
		}
	}
	in, _ := json.Marshal(inputs)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/json_parse.mjs")
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var want []*string
	// sdkjson keeps the lone surrogates JSON.stringify escapes in the messages, as Validate does.
	if err := sdkjson.Unmarshal(raw, &want); err != nil || len(want) != len(inputs) {
		t.Fatalf("decode oracle output: %v (%d of %d)", err, len(want), len(inputs))
	}
	var differences []string
	rejected := 0
	for i, input := range inputs {
		got := Validate([]byte(input))
		switch {
		case want[i] == nil && got == nil:
		case want[i] != nil && got != nil && got.Error() == *want[i]:
			rejected++
		default:
			g, w := "<ok>", "<ok>"
			if got != nil {
				g = got.Error()
			}
			if want[i] != nil {
				w = *want[i]
			}
			differences = append(differences, "input "+strings.ReplaceAll(input, "\n", `\n`)+"\n  go: "+g+"\n  pi: "+w)
		}
	}
	if rejected < len(inputs)/2 {
		t.Fatalf("only %d of %d inputs are rejected by Pi: the corruptions are too weak", rejected, len(inputs))
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 12 {
			shown = shown[:12]
		}
		t.Errorf("%d of %d inputs differ from JSON.parse:\n%s", len(differences), len(inputs), strings.Join(shown, "\n"))
	}
}
