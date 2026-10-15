package codemode

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 24382bed agent-session-mcp.test.ts "tells scripts what directly exposed MCP tools resolve to": the declared result of
// an MCP tool is described two levels deep, `{ content, structuredContent?: { hits }, isError? }`, and a result without
// a declared structured schema keeps the optional field bare.
func TestDescribeOutputDescribesTheFieldsOfObjectFieldsOneLevelDeep(t *testing.T) {
	content := `{"type":"array","items":{"type":"object"}}`
	for name, tc := range map[string]struct{ schema, want string }{
		"structured": {
			`{"type":"object","properties":{"content":` + content + `,"structuredContent":{"type":"object","properties":{"hits":{"type":"array"}},"required":["hits"]},"isError":{"type":"boolean"}},"required":["content"]}`,
			"`{ content, structuredContent?: { hits }, isError? }`",
		},
		"undeclared": {
			`{"type":"object","properties":{"content":` + content + `,"structuredContent":{"type":"object"},"isError":{"type":"boolean"}},"required":["content"]}`,
			"`{ content, structuredContent?, isError? }`",
		},
		"plain object": {`{"type":"object","properties":{"output":{"type":"string"},"exit_code":{"type":"number"}},"required":["output"]}`, "`{ output, exit_code? }`"},
		"third level is not described": {
			`{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"object","properties":{"c":{}}}}}}}`,
			"`{ a?: { b? } }`",
		},
		"string": {`{"type":"string"}`, "a string"},
		"array":  {`{"type":"array","items":{"type":"string"}}`, "`Array<string>`"},
	} {
		got, err := describeOutput(json.RawMessage(tc.schema))
		if err != nil || got != tc.want {
			t.Errorf("%s: describeOutput = %q, %v; want %q", name, got, err, tc.want)
		}
	}
}

// Pi 24382bed tool.ts getScriptReach: every tool for a script that uses a discovery global, else the words of its source.
func TestScriptReachIsAllForDiscoveryGlobalsAndTheWordsOfTheSourceOtherwise(t *testing.T) {
	reach := func(input string) extension.ToolReach { return scriptReach(json.RawMessage(input)) }
	for _, code := range []string{"await searchTools('x')", "describeNamespace('a')", "ALL_TOOLS.map(f)", "x = describeTool"} {
		if got := reach(`{"code":` + string(mustJSON(t, code)) + `}`); !got.All {
			t.Errorf("reach(%q) = %+v, want all", code, got)
		}
	}
	got := reach(`{"code":"await tools.mcp__docs__search({ query: 'q' }); tools[\"mcp__x__\" + name]"}`)
	want := []string{"await", "tools", "mcp__docs__search", "query", "q", "tools", "mcp__x__", "name"}
	if got.All || !reflect.DeepEqual(got.Words, want) {
		t.Errorf("reach(source) = %+v, want words %v", got, want)
	}
	for _, input := range []string{`{}`, `{"code":7}`, `{"code":null}`, `[]`, ``} {
		if got := reach(input); got.All || len(got.Words) != 0 {
			t.Errorf("reach(%q) = %+v, want nothing", input, got)
		}
	}
	if got := reach(`{"code":"searchToolsX describeToolbox"}`); got.All {
		t.Errorf("a word that only contains a discovery global is not one: %+v", got)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
