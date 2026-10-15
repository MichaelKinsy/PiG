package mcp_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Runs Pi's McpClient.listTools (packages/mcp/src/client.ts) and PiG's Client.ListTools against the same scripted tools/list
// pages (client.ts:294-296 listTools over listAll :355-374). Pi and PiG must return the same tools in the same order, send the same
// request params (none for the first page, then each cursor), and fail with the same message when a page or a tool is invalid
// (every page is fetched before any tool is decoded).
func TestClientListToolsMatchesPiOnScriptedPages(t *testing.T) {
	tool := func(name string) map[string]any {
		return map[string]any{"name": name, "description": "d " + name, "inputSchema": map[string]any{"type": "object"}}
	}
	cases := []struct {
		name  string
		pages []any
	}{
		{"two pages, the last ends with an empty cursor", []any{
			map[string]any{"tools": []any{tool("search")}, "nextCursor": "page-2"},
			map[string]any{"tools": []any{tool("read"), map[string]any{"name": "w", "inputSchema": map[string]any{}, "title": "W", "annotations": map[string]any{"readOnlyHint": true}, "_meta": map[string]any{"k": 1}}}, "nextCursor": ""}}},
		{"one empty page", []any{map[string]any{"tools": []any{}}}},
		{"a page with _meta and a null cursor", []any{map[string]any{"tools": []any{tool("a")}, "nextCursor": nil, "_meta": map[string]any{"progressToken": "p"}}}},
		{"an invalid tool on the second page", []any{
			map[string]any{"tools": []any{tool("a")}, "nextCursor": "2"},
			map[string]any{"tools": []any{tool("b"), map[string]any{"name": "no-schema"}}}}},
		{"tools is not an array", []any{map[string]any{"tools": "x"}}},
		{"cursor is not a string", []any{map[string]any{"tools": []any{}, "nextCursor": 5}}},
		{"a repeated cursor", []any{map[string]any{"tools": []any{tool("a")}, "nextCursor": "same"}, map[string]any{"tools": []any{tool("b")}, "nextCursor": "same"}}},
		{"three pages in order", []any{
			map[string]any{"tools": []any{tool("1"), tool("2")}, "nextCursor": "c1"},
			map[string]any{"tools": []any{}, "nextCursor": "c2"},
			map[string]any{"tools": []any{tool("3")}}}},
	}
	var wire []map[string]any
	for _, c := range cases {
		wire = append(wire, map[string]any{"pages": c.pages})
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/list_tools_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi []struct {
		Tools    []json.RawMessage `json:"tools"`
		Requests []any             `json:"requests"`
		Error    string            `json:"error"`
	}
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(cases) {
		t.Fatalf("oracle output: %v", err)
	}
	for i, c := range cases {
		client, server := connect(t)
		var requests []any
		n := 0
		server.setHandler("tools/list", func(request mcp.JSONRPCMessage) (any, error) {
			var params any
			if len(request.Params) > 0 {
				_ = json.Unmarshal(request.Params, &params)
			}
			requests = append(requests, params)
			page := c.pages[min(n, len(c.pages)-1)]
			n++
			return page, nil
		})
		tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
		want := pi[i]
		if want.Error != "" {
			if err == nil || err.Error() != want.Error {
				t.Errorf("%s: PiG error %v, Pi %q", c.name, err, want.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: PiG error %v, Pi returned %d tools", c.name, err, len(want.Tools))
			continue
		}
		if tools == nil {
			t.Errorf("%s: PiG returned nil, Pi returns an array", c.name)
		}
		if len(tools) != len(want.Tools) {
			t.Errorf("%s: PiG %d tools, Pi %d", c.name, len(tools), len(want.Tools))
			continue
		}
		for j, tool := range tools {
			encoded, _ := json.Marshal(tool)
			var got, wantTool any
			_ = json.Unmarshal(encoded, &got)
			_ = json.Unmarshal(want.Tools[j], &wantTool)
			if !reflect.DeepEqual(got, wantTool) {
				t.Errorf("%s: tool %d: PiG %s, Pi %s", c.name, j, encoded, want.Tools[j])
			}
		}
		if !reflect.DeepEqual(requests, want.Requests) {
			t.Errorf("%s: PiG requests %v, Pi %v", c.name, requests, want.Requests)
		}
		_ = client.Close()
	}
}
