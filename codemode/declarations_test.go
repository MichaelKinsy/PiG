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

func typeOf(schema string) string {
	typ, err := codemode.SchemaToType(js(schema), codemode.SchemaToTypeOptions{})
	if err != nil {
		panic(err)
	}
	return typ
}

func mustSchemaToType(t *testing.T, schema json.RawMessage, options codemode.SchemaToTypeOptions) string {
	t.Helper()
	typ, err := codemode.SchemaToType(schema, options)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func mustSignature(t *testing.T, tool codemode.Tool, options codemode.ToolRenderOptions) string {
	t.Helper()
	signature, err := codemode.RenderToolSignature(tool, options)
	if err != nil {
		t.Fatal(err)
	}
	return signature
}

func mustSample(t *testing.T, tool codemode.Tool, options codemode.ToolRenderOptions) string {
	t.Helper()
	sample, err := codemode.RenderToolSample(tool, options)
	if err != nil {
		t.Fatal(err)
	}
	return sample
}

func mustDeclarations(t *testing.T, options codemode.RenderDeclarationsOptions) string {
	t.Helper()
	declarations, err := codemode.RenderDeclarations(options)
	if err != nil {
		t.Fatal(err)
	}
	return declarations
}

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
	check(t, mustSchemaToType(t, schema, codemode.SchemaToTypeOptions{MaxChars: new(100)}), "unknown")
	if got := mustSchemaToType(t, schema, codemode.SchemaToTypeOptions{}); !strings.Contains(got, "field49?: string;") {
		t.Errorf("unbudgeted type = %q", got)
	}
}

func TestRenderToolSignatureRendersSignaturesWithNormalizedIdentifiers(t *testing.T) {
	check(t, mustSignature(t, codemode.Tool{
		Name:         "hidden-dynamic-tool",
		InputSchema:  js(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`),
		OutputSchema: js(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`),
	}, codemode.ToolRenderOptions{}), "hidden_dynamic_tool(args: { city: string; }): Promise<{ ok: boolean; }>;")
	check(t, mustSignature(t, codemode.Tool{Name: "free"}, codemode.ToolRenderOptions{}), "free(args: unknown): Promise<unknown>;")
}

func TestRenderToolSignatureRendersMCPCallToolResultOutputSchemasAsCallToolResult(t *testing.T) {
	input := js(`{"type":"object","properties":{},"additionalProperties":false}`)
	structured := `{"type":"object","properties":{"results":{"type":"array","items":{"$ref":"#/definitions/Result~1item~0v1"}}},"required":["results"],"additionalProperties":false,
		"definitions":{"Result/item~v1":{"type":"object","properties":{"id":{"type":"string"},"score":{"type":"number"}},"required":["id","score"],"additionalProperties":false}}}`
	check(t, mustSignature(t, codemode.Tool{Name: "mcp__sample__search", InputSchema: input, OutputSchema: js(mcpResultSchema(structured))}, codemode.ToolRenderOptions{}),
		"mcp__sample__search(args: {}): Promise<CallToolResult<{ results: Array<{ id: string; score: number; }>; }>>;")
	check(t, mustSignature(t, codemode.Tool{Name: "plain", InputSchema: input, OutputSchema: js(mcpResultSchema(""))}, codemode.ToolRenderOptions{}),
		"plain(args: {}): Promise<CallToolResult>;")
	if got := codemode.McpStructuredContentSchema(js(`{"type":"object","properties":{"content":{"type":"array"}}}`)); got != nil {
		t.Errorf("McpStructuredContentSchema(non-MCP) = %s, want undefined", got)
	}
}

func TestRenderToolSampleRendersThePerToolSample(t *testing.T) {
	check(t, mustSample(t, codemode.Tool{Name: "foo", Description: "bar", InputSchema: js(`{"type":"string"}`)}, codemode.ToolRenderOptions{}),
		"bar\n\ncodemode tool declaration:\n```ts\ndeclare const tools: { foo(args: string): Promise<unknown>; };\n```")
}

func TestRenderDeclarationsRendersToolsAndGlobals(t *testing.T) {
	text := mustDeclarations(t, codemode.RenderDeclarationsOptions{
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

// Pi source: packages/codemode/src/types.ts
// mutation-checked: dropping the reads and writes of Tool.Signature fails it
func TestRenderDeclarationsRendersNamespacedGlobalsAndExplicitSignatures(t *testing.T) {
	text := mustDeclarations(t, codemode.RenderDeclarationsOptions{Globals: []codemode.Tool{
		{Name: "models.list", Description: "List models.", Signature: new("(type: string): Promise<string[]>"), Execute: execNothing},
		{Name: "models.get", InputSchema: js(`{"type":"string"}`), Execute: execNothing},
		{Name: "plain", Signature: new("(): void"), Execute: execNothing},
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
	text := mustDeclarations(t, codemode.RenderDeclarationsOptions{Tools: []codemode.Tool{{Name: "x", Description: "a */ b", Execute: execNothing}}})
	if !strings.Contains(text, "/** a *\\/ b */") {
		t.Errorf("text = %q", text)
	}
}

// declarations.ts schemaToType: `options.maxChars !== undefined && type.length > options.maxChars` — an explicit zero limits
// every non-empty type, while an absent limit does not. renderToolSignature defaults only an absent inputMaxChars.
func TestDeclarationOptionsDistinguishAbsentFromZero(t *testing.T) {
	check(t, mustSchemaToType(t, js(`{"type":"string"}`), codemode.SchemaToTypeOptions{}), "string")
	check(t, mustSchemaToType(t, js(`{"type":"string"}`), codemode.SchemaToTypeOptions{MaxChars: new(6)}), "string")
	check(t, mustSchemaToType(t, js(`{"type":"string"}`), codemode.SchemaToTypeOptions{MaxChars: new(5)}), "unknown")
	check(t, mustSchemaToType(t, js(`{"type":"string"}`), codemode.SchemaToTypeOptions{MaxChars: new(0)}), "unknown")
	tool := codemode.Tool{Name: "t", InputSchema: js(`{"type":"string"}`)}
	check(t, mustSignature(t, tool, codemode.ToolRenderOptions{}), "t(args: string): Promise<unknown>;")
	check(t, mustSignature(t, tool, codemode.ToolRenderOptions{InputMaxChars: new(0)}), "t(args: unknown): Promise<unknown>;")
	check(t, mustSample(t, tool, codemode.ToolRenderOptions{InputMaxChars: new(0)}), "\n\ncodemode tool declaration:\n```ts\ndeclare const tools: { t(args: unknown): Promise<unknown>; };\n```")
}

// Pi's resolveRef decodes each $ref segment with decodeURIComponent, which throws a URIError for a malformed escape or for
// escapes that do not form UTF-8. The error leaves every render function that expands a schema, rather than the reference
// rendering as `unknown`. A well-formed reference still resolves.
func TestMalformedRefPercentEncodingIsAnError(t *testing.T) {
	bad := []string{`#/$defs/%E0%A4%A`, `#/$defs/%`, `#/$defs/%FF`}
	for _, ref := range bad {
		schema := js(`{"type":"object","properties":{"a":{"$ref":"` + ref + `"}},"$defs":{}}`)
		tool := codemode.Tool{Name: "t", InputSchema: schema, OutputSchema: schema}
		if _, err := codemode.SchemaToType(schema, codemode.SchemaToTypeOptions{}); err == nil {
			t.Errorf("SchemaToType(%s): want an error", ref)
		}
		if _, err := codemode.RenderToolSignature(tool, codemode.ToolRenderOptions{}); err == nil {
			t.Errorf("RenderToolSignature(%s): want an error", ref)
		}
		if _, err := codemode.RenderToolSample(tool, codemode.ToolRenderOptions{}); err == nil {
			t.Errorf("RenderToolSample(%s): want an error", ref)
		}
		if _, err := codemode.RenderToolOutputType(schema); err == nil {
			t.Errorf("RenderToolOutputType(%s): want an error", ref)
		}
		if _, err := codemode.RenderDeclarations(codemode.RenderDeclarationsOptions{Tools: []codemode.Tool{tool}}); err == nil {
			t.Errorf("RenderDeclarations tools(%s): want an error", ref)
		}
		if _, err := codemode.RenderDeclarations(codemode.RenderDeclarationsOptions{Globals: []codemode.Tool{tool}}); err == nil {
			t.Errorf("RenderDeclarations globals(%s): want an error", ref)
		}
	}
	good := js(`{"type":"object","properties":{"a":{"$ref":"#/$defs/a%20b"}},"$defs":{"a b":{"type":"string"}}}`)
	if got := mustSchemaToType(t, good, codemode.SchemaToTypeOptions{}); got != "{ a?: string; }" {
		t.Errorf("well-formed escaped reference = %q", got)
	}
}
