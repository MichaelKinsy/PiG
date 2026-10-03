package codemode_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// Ports packages/codemode/test/declarations.test.ts (v0.99.1).

func execNothing(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }

func typeOf(schema string) string { return codemode.SchemaToType(js(schema), 0) }

func check(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func mcpResultSchema(structuredContent string) string {
	extra := ""
	if structuredContent != "" {
		extra = `"structuredContent": ` + structuredContent + `,`
	}
	return `{"type":"object","properties":{"content":{"type":"array","items":{"type":"object"}},` + extra + `"isError":{"type":"boolean"},"_meta":{"type":"object"}},"required":["content"]}`
}

func TestSchemaToTypeRendersPrimitivesLiteralsAndUnions(t *testing.T) {
	check(t, typeOf(`{"type":"string"}`), "string")
	check(t, typeOf(`{"type":"integer"}`), "number")
	check(t, typeOf(`{"type":["string","null"]}`), "string | null")
	check(t, typeOf(`{"const":"a"}`), `"a"`)
	check(t, typeOf(`{"enum":["a",1,null]}`), `"a" | 1 | null`)
	check(t, typeOf(`{"anyOf":[{"type":"string"},{"type":"number"}]}`), "string | number")
	check(t, typeOf(`{"anyOf":[{"type":"string"},{}]}`), "unknown")
	check(t, typeOf(`{"allOf":[{"anyOf":[{"type":"string"},{"type":"number"}]},{"const":1}]}`), "(string | number) & 1")
	check(t, typeOf(`{"$ref":"#/defs/x"}`), "unknown")
	check(t, typeOf(`true`), "unknown")
	check(t, typeOf(`false`), "never")
}

func TestSchemaToTypeRendersObjectsOnOneLineWithSortedProperties(t *testing.T) {
	check(t, typeOf(`{"type":"object","properties":{"city":{"type":"string"},"max-lines":{"type":"number"}},"required":["city"],"additionalProperties":false}`),
		`{ city: string; "max-lines"?: number; }`)
	check(t, typeOf(`{"type":"object","additionalProperties":{"type":"number"}}`), "{ [key: string]: number; }")
	check(t, typeOf(`{"type":"object"}`), "{ [key: string]: unknown; }")
	check(t, typeOf(`{"type":"object","properties":{},"additionalProperties":false}`), "{}")
}

func TestSchemaToTypePutsPropertyDescriptionsOnCommentLines(t *testing.T) {
	check(t, typeOf(`{"type":"object","properties":{"weather":{"type":"array","description":"look up weather for a given list of locations","items":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}},"required":["weather"]}`),
		"{\n  // look up weather for a given list of locations\n  weather: Array<{ location: string; }>;\n}")
	check(t, typeOf(`{"type":"object","properties":{"outer":{"type":"object","description":"Outer","properties":{"inner":{"type":"string","description":"Inner"}}}}}`),
		"{\n  // Outer\n  outer?: {\n    // Inner\n    inner?: string;\n  };\n}")
}

func TestSchemaToTypeResolvesLocalReferencesAndStopsAtRecursiveOnes(t *testing.T) {
	check(t, typeOf(`{
		"type":"object",
		"properties":{"item":{"$ref":"#/$defs/Item"},"legacy":{"$ref":"#/definitions/Legacy"},"remote":{"$ref":"https://example.com/schema.json"}},
		"required":["item"],
		"$defs":{"Item":{"type":"object","properties":{"id":{"type":"string"},"parent":{"$ref":"#/$defs/Item"}},"required":["id"]}},
		"definitions":{"Legacy":{"enum":["a","b"]}}}`),
		`{ item: { id: string; parent?: unknown; }; legacy?: "a" | "b"; remote?: unknown; }`)
}

func TestSchemaToTypeRendersArraysAndTuples(t *testing.T) {
	check(t, typeOf(`{"type":"array","items":{"type":"string"}}`), "Array<string>")
	check(t, typeOf(`{"type":"array","prefixItems":[{"type":"string"},{"type":"number"}]}`), "[string, number]")
	check(t, typeOf(`{"type":"array"}`), "unknown[]")
}

func TestSchemaToTypeRendersTypesOverTheBudgetAsUnknown(t *testing.T) {
	var properties []string
	for i := range 50 {
		properties = append(properties, fmt.Sprintf(`"field%d":{"type":"string"}`, i))
	}
	schema := js(`{"type":"object","properties":{` + strings.Join(properties, ",") + `}}`)
	check(t, codemode.SchemaToType(schema, 100), "unknown")
	if got := codemode.SchemaToType(schema, 0); !strings.Contains(got, "field49?: string;") {
		t.Errorf("unbudgeted type = %q", got)
	}
}

func TestRenderToolSignatureRendersSignaturesWithNormalizedIdentifiers(t *testing.T) {
	check(t, codemode.RenderToolSignature(codemode.Tool{
		Name:         "hidden-dynamic-tool",
		InputSchema:  js(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`),
		OutputSchema: js(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`),
	}, 0), "hidden_dynamic_tool(args: { city: string; }): Promise<{ ok: boolean; }>;")
	check(t, codemode.RenderToolSignature(codemode.Tool{Name: "free"}, 0), "free(args: unknown): Promise<unknown>;")
}

func TestRenderToolSignatureRendersMCPCallToolResultOutputSchemasAsCallToolResult(t *testing.T) {
	input := js(`{"type":"object","properties":{},"additionalProperties":false}`)
	structured := `{"type":"object","properties":{"results":{"type":"array","items":{"$ref":"#/definitions/Result~1item~0v1"}}},"required":["results"],"additionalProperties":false,
		"definitions":{"Result/item~v1":{"type":"object","properties":{"id":{"type":"string"},"score":{"type":"number"}},"required":["id","score"],"additionalProperties":false}}}`
	check(t, codemode.RenderToolSignature(codemode.Tool{Name: "mcp__sample__search", InputSchema: input, OutputSchema: js(mcpResultSchema(structured))}, 0),
		"mcp__sample__search(args: {}): Promise<CallToolResult<{ results: Array<{ id: string; score: number; }>; }>>;")
	check(t, codemode.RenderToolSignature(codemode.Tool{Name: "plain", InputSchema: input, OutputSchema: js(mcpResultSchema(""))}, 0),
		"plain(args: {}): Promise<CallToolResult>;")
	if got := codemode.McpStructuredContentSchema(js(`{"type":"object","properties":{"content":{"type":"array"}}}`)); got != nil {
		t.Errorf("McpStructuredContentSchema(non-MCP) = %s, want undefined", got)
	}
}

func TestRenderToolSampleRendersThePerToolSample(t *testing.T) {
	check(t, codemode.RenderToolSample(codemode.Tool{Name: "foo", Description: "bar", InputSchema: js(`{"type":"string"}`)}, 0),
		"bar\n\ncodemode tool declaration:\n```ts\ndeclare const tools: { foo(args: string): Promise<unknown>; };\n```")
}

func TestRenderDeclarationsRendersToolsAndGlobals(t *testing.T) {
	text := codemode.RenderDeclarations(codemode.RenderDeclarationsOptions{
		Tools: []codemode.Tool{
			{Name: "read", Description: "Read a file.\nSecond line.", InputSchema: js(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`), OutputSchema: js(`{"type":"string"}`), Execute: execNothing},
			{Name: "remote-api", Execute: execNothing},
		},
		Globals: []codemode.Tool{{Name: "attach", Description: "Attach it.", InputSchema: js(`{"type":"string"}`), Execute: execNothing}},
	})
	check(t, text, strings.Join([]string{
		"declare const tools: {",
		"  /**",
		"   * Read a file.",
		"   * Second line.",
		"   */",
		"  read(args: { path: string; }): Promise<string>;",
		"  remote_api(args: unknown): Promise<unknown>;",
		"};",
		"",
		"/** Attach it. */",
		"declare function attach(args: string): Promise<unknown>;",
	}, "\n"))
}

func TestRenderDeclarationsRendersNamespacedGlobalsAndExplicitSignatures(t *testing.T) {
	text := codemode.RenderDeclarations(codemode.RenderDeclarationsOptions{Globals: []codemode.Tool{
		{Name: "models.list", Description: "List models.", Signature: "(type: string): Promise<string[]>", Execute: execNothing},
		{Name: "models.get", InputSchema: js(`{"type":"string"}`), Execute: execNothing},
		{Name: "plain", Signature: "(): void", Execute: execNothing},
	}})
	check(t, text, strings.Join([]string{
		"declare function plain(): void;",
		"",
		"declare const models: {",
		"  /** List models. */",
		"  list(type: string): Promise<string[]>;",
		"  get(args: string): Promise<unknown>;",
		"};",
	}, "\n"))
}

func TestRenderDeclarationsEscapesCommentTerminatorsInDescriptions(t *testing.T) {
	text := codemode.RenderDeclarations(codemode.RenderDeclarationsOptions{Tools: []codemode.Tool{{Name: "x", Description: "a */ b", Execute: execNothing}}})
	if !strings.Contains(text, "/** a *\\/ b */") {
		t.Errorf("text = %q", text)
	}
}
