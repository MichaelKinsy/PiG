package codemode_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/codemode"
)

// resolveSchemaRefJSONByTokens is the one-pass resolver as it read root through json.Decoder.Token, which boxes every token. The byte scanner must
// agree with it on every text, valid or not.
func resolveSchemaRefJSONByTokens(ref string, root json.RawMessage) (json.RawMessage, bool) {
	if ref != "#" && !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var segments []string
	if ref != "#" {
		for segment := range strings.SplitSeq(ref[2:], "/") {
			if segment == "" {
				continue
			}
			segments = append(segments, strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~"))
		}
	}
	current := root
	if len(segments) > 0 {
		var ok bool
		var err error
		if current, ok, err = walkTokens(json.NewDecoder(bytes.NewReader(root)), segments); err != nil || !ok {
			return nil, false
		}
	}
	switch trimmed := strings.TrimLeft(string(current), " \t\r\n"); {
	case strings.HasPrefix(trimmed, "{"), strings.HasPrefix(trimmed, "true"), strings.HasPrefix(trimmed, "false"):
		return current, true
	}
	return nil, false
}

func walkTokens(decoder *json.Decoder, segments []string) (json.RawMessage, bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, false, err
	}
	if delim, isDelim := token.(json.Delim); !isDelim || delim != '{' {
		return nil, false, skipTokens(decoder, token)
	}
	var found json.RawMessage
	var ok bool
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, false, err
		}
		if key, _ := keyToken.(string); key != segments[0] {
			valueToken, err := decoder.Token()
			if err != nil {
				return nil, false, err
			}
			if err := skipTokens(decoder, valueToken); err != nil {
				return nil, false, err
			}
			continue
		}
		if len(segments) > 1 {
			if found, ok, err = walkTokens(decoder, segments[1:]); err != nil {
				return nil, false, err
			}
			continue
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, false, err
		}
		found, ok = raw, true
	}
	_, err = decoder.Token()
	return found, ok, err
}

func skipTokens(decoder *json.Decoder, token json.Token) error {
	delim, isDelim := token.(json.Delim)
	if !isDelim || (delim != '{' && delim != '[') {
		return nil
	}
	for depth := 1; depth > 0; {
		next, err := decoder.Token()
		if err != nil {
			return err
		}
		if d, ok := next.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

func tokensCorpus(f interface{ Add(...any) }) {
	for _, root := range []string{
		`{"a":{"b":{"c":1}}}`, `{"a":1,"a":{"b":true}}`, ` {"a" : {"b" : false} } x`, `{"a":{"b":{}},}`, `{"a":[1,2,]}`,
		`{"\u0061":{"b":{}}}`, `{"a":{"b":01}}`, `{"a":{"b":1e}}`, `{"a":{"b":-}}`, `{"a":{"b":"\x"}}`, "{\"a\":\"\x01\"}",
		`{"é":{"b":{}}}`, "{\"\xff\":{\"b\":{}}}", `{"a":{"b":nul}}`, `{"a":{"b":{}}`, `{"a":{"b":[{"a":1}]}}`, `{"a":{"b":tru}}`, `[]`, `{"a":{"b":1.5E+3}}`,
		`{"a":{"b":"\ud800"}}`, `{"a":{"b":{"c":[[[[]]]]}}}`, `{"a":[{"b":1},{"a"}]}`, `{"a":{"b":{}} "c":1}`,
	} {
		for _, ref := range []string{"#/a/b", "#/a", "#", "#/\u00e9/b", "#/\ufffd/b", "#/a/b/c", "#/c"} {
			f.Add(ref, root)
		}
	}
}

// FuzzResolveSchemaRefJSONAgainstTokens checks the byte scanner against the Decoder.Token resolver on arbitrary text, malformed text included.
func FuzzResolveSchemaRefJSONAgainstTokens(f *testing.F) {
	tokensCorpus(f)
	f.Fuzz(func(t *testing.T, ref, root string) {
		if strings.Contains(ref, "%") || !utf8.ValidString(ref) {
			return
		}
		got, ok := codemode.ResolveSchemaRefJSON(ref, json.RawMessage(root))
		want, wantOK := resolveSchemaRefJSONByTokens(ref, json.RawMessage(root))
		if ok != wantOK || (ok && !bytes.Equal(got, want)) {
			t.Fatalf("ref %q root %q: got %q %v, tokens %q %v", ref, root, got, ok, want, wantOK)
		}
	})
}

// TestResolveSchemaRefJSONDeepNesting checks the byte scanner against the Decoder.Token resolver on deeply nested text. The two differ only at
// nesting within two levels of encoding/json's limit of 10000, which Decoder.Token rejects: the loop stops short of that.
func TestResolveSchemaRefJSONDeepNesting(t *testing.T) {
	for depth := 9900; depth <= 9990; depth += 3 {
		deepObj := strings.Repeat(`{"c":`, depth-1) + `{}` + strings.Repeat("}", depth-1)
		deepArr := strings.Repeat("[", depth) + strings.Repeat("]", depth)
		for n, c := range []struct{ ref, root string }{
			{"#/a/b", `{"a":{"b":` + deepObj + `}}`},
			{"#/a/b", `{"a":` + deepArr + `,"c":{"b":{}}}`},
			{"#/a/b", `{"a":{"b":{"c":` + deepArr + `}}}`},
			{"#/b", `{"b":` + deepObj + `}`},
			{"#/b", `{"x":` + deepObj + `,"b":{}}`},
			{"#/a/b", `{"a":{"b":` + deepObj + `},"x":` + deepArr + `}`},
		} {
			got, ok := codemode.ResolveSchemaRefJSON(c.ref, json.RawMessage(c.root))
			want, wantOK := resolveSchemaRefJSONByTokens(c.ref, json.RawMessage(c.root))
			if ok != wantOK || !bytes.Equal(got, want) {
				t.Errorf("depth %d case %d: got %v, tokens %v", depth, n, ok, wantOK)
			}
		}
	}
}

// TestResolveSchemaRefJSONHasNoNestingLimit pins that nesting is not limited. Pi reads schemas with JSON.parse, which has no limit, where
// encoding/json stops at 10000 levels: a deep schema resolves in both.
func TestResolveSchemaRefJSONHasNoNestingLimit(t *testing.T) {
	for _, depth := range []int{10000, 10001, 20000, 100000} {
		deepObj := strings.Repeat(`{"c":`, depth-1) + `{}` + strings.Repeat("}", depth-1)
		deepArr := strings.Repeat("[", depth) + strings.Repeat("]", depth)
		for _, c := range []struct {
			name, ref, root, want string
			ok                    bool
		}{
			{"found object", "#/a/b", `{"a":{"b":` + deepObj + `}}`, deepObj, true},
			{"found beside deep array", "#/a/b", `{"a":{"b":{"c":` + deepArr + `}}}`, `{"c":` + deepArr + `}`, true},
			{"deep object skipped", "#/b", `{"x":` + deepObj + `,"b":{}}`, `{}`, true},
			{"deep array skipped", "#/b", `{"x":` + deepArr + `,"b":true}`, `true`, true},
			{"found array names nothing", "#/a", `{"a":` + deepArr + `}`, "", false},
			{"unclosed", "#/a", `{"a":` + deepObj[:len(deepObj)-1] + `}`, "", false},
			{"mismatched", "#/a", `{"a":` + strings.Repeat("[", depth) + strings.Repeat("}", depth) + `}`, "", false},
		} {
			got, ok := codemode.ResolveSchemaRefJSON(c.ref, json.RawMessage(c.root))
			if ok != c.ok || string(got) != c.want {
				t.Errorf("depth %d, %s: got %v (%d bytes), want %v (%d bytes)", depth, c.name, ok, len(got), c.ok, len(c.want))
			}
		}
	}
}

// BenchmarkResolveSchemaRefJSONByTokens is the same on the Decoder.Token resolver, the before side of the comparison with the byte scanner.
func BenchmarkResolveSchemaRefJSONByTokens(b *testing.B) {
	benchResolve(b, resolveSchemaRefJSONByTokens)
}
