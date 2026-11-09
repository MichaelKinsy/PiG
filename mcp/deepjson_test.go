package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Pi parses with JSON.parse, which reads a schema or result nested 12,000 levels deep; encoding/json refuses more than 10,000.
const deepLevels = 12000

func deepObject(levels int) string {
	return strings.Repeat(`{"a":`, levels) + `1` + strings.Repeat("}", levels)
}

func TestParseWireMessageKeepsAToolSchemaNested12000LevelsDeep(t *testing.T) {
	schema := deepObject(deepLevels)
	line := `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"t","description":"d","inputSchema":{"type":"object","properties":{"p":` + schema + `}},"annotations":{"readOnlyHint":true},"_meta":{"m":` + schema + `}}]}}`
	message, err := parseWireMessage([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	page, err := validateListPage("tools/list", "tools", message.Result, func(map[string]json.RawMessage) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	tool, err := decodeItem[Tool](page.raws[0], "tools/list")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"p":` + schema + `}}`
	if string(tool.InputSchema) != want {
		t.Fatalf("inputSchema is %d bytes, want %d", len(tool.InputSchema), len(want))
	}
	if string(tool.Meta) != `{"m":`+schema+`}` {
		t.Fatalf("_meta lost (%d bytes)", len(tool.Meta))
	}
	if tool.Annotations == nil || tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
		t.Fatal("annotations lost")
	}
}

func TestParseWireMessageKeepsACallResultNested12000LevelsDeep(t *testing.T) {
	deep := deepObject(deepLevels)
	line := `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"x","_meta":` + deep + `}],"structuredContent":` + deep + `}}`
	message, err := parseWireMessage([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	result, err := validateCallToolResult(message.Result)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "x" || string(result.Content[0].Meta) != deep {
		t.Fatalf("content = %d blocks", len(result.Content))
	}
	if !bytes.Equal(result.Content[0].raw, []byte(`{"type":"text","text":"x","_meta":`+deep+`}`)) {
		t.Fatal("the kept bytes of the block lost their depth")
	}
	if string(result.StructuredContent) != deep {
		t.Fatal("structuredContent lost")
	}
}

func TestParseWireMessageRejectsMalformedTextNested12000LevelsDeep(t *testing.T) {
	deep := deepObject(deepLevels)
	for name, line := range map[string]string{
		"trailing comma": `{"jsonrpc":"2.0","id":1,"result":{"x":` + deep + `,}}`,
		"control byte":   `{"jsonrpc":"2.0","id":1,"result":{"x":"` + "\x01" + `","y":` + deep + `}}`,
		"unclosed":       `{"jsonrpc":"2.0","id":1,"result":{"x":` + deep[:len(deep)-1],
		"bad literal":    `{"jsonrpc":"2.0","id":1,"result":{"x":` + deep + `,"y":nul}}`,
		// The flaws below sit inside the part nested more than 32 levels down, where the fallback checks the text itself.
		"control byte in the deep part":   `{"jsonrpc":"2.0","id":1,"result":{"x":` + strings.Repeat(`{"a":`, deepLevels) + `"` + "\x01" + `"` + strings.Repeat("}", deepLevels) + `}}`,
		"mismatched closer after a value": `{"jsonrpc":"2.0","id":1,"result":{"x":` + strings.Repeat("[", deepLevels) + `1}` + strings.Repeat("]", deepLevels-1) + `}}`,
		"mismatched closer":               `{"jsonrpc":"2.0","id":1,"result":{"x":` + strings.Repeat("[", deepLevels) + `}` + strings.Repeat("]", deepLevels-1) + `}}`,
		"trailing comma in the deep part": `{"jsonrpc":"2.0","id":1,"result":{"x":` + strings.Repeat(`{"a":`, deepLevels) + `[1,]` + strings.Repeat("}", deepLevels) + `}}`,
		"bad escape in the deep part":     `{"jsonrpc":"2.0","id":1,"result":{"x":` + strings.Repeat(`[`, deepLevels) + `"\x"` + strings.Repeat("]", deepLevels) + `}}`,
		"bad number in the deep part":     `{"jsonrpc":"2.0","id":1,"result":{"x":` + strings.Repeat(`[`, deepLevels) + `01` + strings.Repeat("]", deepLevels) + `}}`,
		"missing colon in the deep part":  `{"jsonrpc":"2.0","id":1,"result":{"x":` + strings.Repeat(`[`, deepLevels) + `{"a"1}` + strings.Repeat("]", deepLevels) + `}}`,
	} {
		if _, err := parseWireMessage([]byte(line)); err == nil {
			t.Errorf("%s: accepted by parseWireMessage", name)
		}
		// ParseJSONRPCMessage alone, without the syntax check parseWireMessage runs first: the fallback must reject the text itself.
		if _, err := ParseJSONRPCMessage([]byte(line)); err == nil {
			t.Errorf("%s: accepted by ParseJSONRPCMessage", name)
		}
	}
}

// Differential: wherever encoding/json decodes the text, deepFallback gives the same members; where it does not, deepFallback accepts only text
// that is JSON once the depth is ignored, and keeps every member as the original bytes.
func FuzzDeepFallbackMatchesEncodingJSON(f *testing.F) {
	for _, s := range []string{`{"a":1}`, `{"a":[1,{"b":"c"}],"d":null}`, `[1,2,]`, `{"a":"\u0000pigdeep0"}`, `{"\u0000pigdeep0":0}`, strings.Repeat(`[`, 40) + strings.Repeat(`]`, 40), `{"a":` + strings.Repeat(`{"b":`, 40) + `1` + strings.Repeat(`}`, 40) + `}`, `{"a":1e400}`, "{\"a\":\"\xff\"}", `{"a":1} x`, ` {"a" : [ ] } `, "{\"a\":\"\x01\"}", `{"a":[}`} {
		f.Add([]byte(s), uint8(0))
		f.Add([]byte(s), uint8(3))
	}
	f.Fuzz(func(t *testing.T, data []byte, wrap uint8) {
		// wrap%4 == 3 nests the text 10010 levels deep inside a member, past encoding/json's limit.
		if wrap%4 == 3 {
			data = []byte(`{"w":` + strings.Repeat(`[`, 10010) + string(data) + strings.Repeat(`]`, 10010) + `}`)
		}
		var got, want map[string]json.RawMessage
		gotErr := deepFallback(json.Unmarshal(data, &got), data, &got, func(b []byte) error { return json.Unmarshal(b, &got) })
		wantErr := json.Unmarshal(data, &want)
		if wantErr == nil {
			if gotErr != nil || len(got) != len(want) {
				t.Fatalf("%q: deepFallback %v / %d members, encoding/json ok / %d", data, gotErr, len(got), len(want))
			}
			for k, v := range want {
				if !bytes.Equal(got[k], v) {
					t.Fatalf("%q: member %q differs", data, k)
				}
			}
			return
		}
		if gotErr == nil {
			var shallow any
			elided, spans, _, ok := elideDeep(data)
			if !ok || len(spans) == 0 || json.Unmarshal(elided, &shallow) != nil {
				t.Fatalf("%q: accepted, encoding/json error %v", data, wantErr)
			}
			for k, v := range got {
				if !bytes.Contains(data, v) {
					t.Fatalf("%q: member %q is not original text", data, k)
				}
			}
		}
	})
}
