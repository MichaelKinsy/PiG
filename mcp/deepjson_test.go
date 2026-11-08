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
	} {
		if _, err := parseWireMessage([]byte(line)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
