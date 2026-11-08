package codemode_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// resolveSchemaRefJSONByParsing is the reference: it parses each level with orderedjson, the way ResolveSchemaRefJSON did before it read root once.
func resolveSchemaRefJSONByParsing(ref string, root json.RawMessage) (json.RawMessage, bool) {
	if ref != "#" && !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	current := root
	if ref != "#" {
		for segment := range strings.SplitSeq(ref[2:], "/") {
			if segment == "" {
				continue
			}
			segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
			members, err := orderedjson.Parse(current)
			if err != nil {
				return nil, false
			}
			var ok bool
			if current, ok = members.Get(segment); !ok {
				return nil, false
			}
		}
	}
	switch trimmed := strings.TrimLeft(string(current), " \t\r\n"); {
	case strings.HasPrefix(trimmed, "{"), strings.HasPrefix(trimmed, "true"), strings.HasPrefix(trimmed, "false"):
		return current, true
	}
	return nil, false
}

// FuzzResolveSchemaRefJSON checks that the one-pass resolver never panics and agrees with the parse-per-level reference on valid schema text.
func FuzzResolveSchemaRefJSON(f *testing.F) {
	f.Add("#/$defs/A", `{"$defs":{"A":{"type":"string"}}}`)
	f.Add("#/a/b", `{"a":{"b":true},"a ":1}`)
	f.Add("#/a", `{"a":1,"a":{"x":[1,{"a":2}]}}`)
	f.Add("#/a%zz", `{"a":{}}`)
	f.Add("#", `{"a":{}}`)
	f.Add("#/a/0", `{"a":[{}]}`)
	f.Fuzz(func(t *testing.T, ref, root string) {
		got, ok := codemode.ResolveSchemaRefJSON(ref, json.RawMessage(root))
		if !json.Valid([]byte(root)) || strings.Contains(ref, "%") {
			return
		}
		if ok && !json.Valid(got) {
			t.Fatalf("resolved text %q is not JSON", got)
		}
		want, wantOK := resolveSchemaRefJSONByParsing(ref, json.RawMessage(root))
		if ok != wantOK || (ok && strings.TrimSpace(string(got)) != strings.TrimSpace(string(want))) {
			t.Fatalf("ref %q root %q: got %q %v, reference %q %v", ref, root, got, ok, want, wantOK)
		}
	})
}

// BenchmarkResolveSchemaRefJSON resolves a deep local ref in a wide schema.
func BenchmarkResolveSchemaRefJSON(b *testing.B) { benchResolve(b, codemode.ResolveSchemaRefJSON) }

// BenchmarkResolveSchemaRefJSONByParsing is the same on the parse-per-level reference, the before side of the comparison.
func BenchmarkResolveSchemaRefJSONByParsing(b *testing.B) {
	benchResolve(b, resolveSchemaRefJSONByParsing)
}

func benchResolve(b *testing.B, resolve func(string, json.RawMessage) (json.RawMessage, bool)) {
	var sb strings.Builder
	sb.WriteString(`{"$defs":{`)
	for i := range 200 {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`"d`)
		sb.WriteString(strings.Repeat("x", i%7))
		sb.WriteString(`":{"properties":{"p":{"properties":{"q":{"type":"string","description":"` + strings.Repeat("filler ", 40) + `"}}}}}`)
		sb.WriteString(`,"z` + strings.Repeat("y", i) + `":{"a":{"b":{"c":{"d":{"e":true}}}}}`)
	}
	sb.WriteString(`,"target":{"a":{"b":{"c":{"d":{"e":{"type":"integer"}}}}}}}}`)
	root := json.RawMessage(sb.String())
	b.ReportAllocs()
	b.SetBytes(int64(len(root)))
	for b.Loop() {
		if _, ok := resolve("#/$defs/target/a/b/c/d/e", root); !ok {
			b.Fatal("not found")
		}
	}
}
