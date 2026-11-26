package mcpext_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Pins MCP model-content conversion and script-result edge cases
// (packages/mcp/src/model-content.ts, packages/coding-agent/src/extensions/mcp/tools.ts):
//  1. ToMcpModelContent omits binary resources when SaveOutput is nil, whereas ConvertMcpResult defaults SaveOutput to SaveToTempFile.
//  2. ConvertMcpResult drops `_meta` and serializes a nil Content slice as `[]` when CallToolResult.Raw is nil.
//  3. Resource link sizes format with JavaScript number/toFixed semantics without float-to-int truncation.
func TestMCPResultSchemaAndModelContentObservables(t *testing.T) {
	t.Run("ToMcpModelContent omits binary resources without a saver while ConvertMcpResult defaults to SaveToTempFile", func(t *testing.T) {
		pdf := mcp.ContentBlock{
			Type: mcp.ContentResource,
			Resource: &mcp.ResourceContents{
				URI:      "file:///r/report.pdf",
				MimeType: "application/pdf",
				Blob:     new("JVBERg=="),
			},
		}
		omitted := mcp.ToMcpModelContent("docs", mcp.CallToolResult{Content: []mcp.ContentBlock{pdf}}, mcp.McpModelContentOptions{})
		if len(omitted) != 1 || omitted[0].Text != "[binary resource file:///r/report.pdf (application/pdf) omitted]" {
			t.Fatalf("ToMcpModelContent without saver = %#v", omitted)
		}
		converted, err := mcpext.ConvertMcpResult("docs", "get", &mcp.CallToolResult{Content: []mcp.ContentBlock{pdf}}, mcpext.ConvertMcpResultOptions{})
		if err != nil {
			t.Fatalf("ConvertMcpResult: %v", err)
		}
		if len(converted.Content) != 1 {
			t.Fatalf("ConvertMcpResult default saver: len(content) = %d, want 1 (%#v)", len(converted.Content), converted.Content)
		}
		gotText := converted.Content[0].(ai.TextContent).Text
		const prefix = "[Binary resource file:///r/report.pdf (application/pdf, 4B) saved to "
		if !strings.HasPrefix(gotText, prefix) || !strings.HasSuffix(gotText, ".pdf]") {
			t.Fatalf("ConvertMcpResult default saver text = %q, want prefix %q ending .pdf]", gotText, prefix)
		}
		savedPath := strings.TrimSuffix(strings.TrimPrefix(gotText, prefix), "]")
		t.Cleanup(func() { _ = os.Remove(savedPath) })
		data, err := os.ReadFile(savedPath)
		if err != nil {
			t.Fatalf("read saved temp file %q: %v", savedPath, err)
		}
		if string(data) != "%PDF" {
			t.Fatalf("saved temp file bytes = %q, want %q", string(data), "%PDF")
		}
	})

	t.Run("ConvertMcpResult script result drops _meta and keeps content array for nil Content slice", func(t *testing.T) {
		isErr := true
		res := &mcp.CallToolResult{
			Content:           nil,
			StructuredContent: json.RawMessage(`{"hits":2}`),
			IsError:           &isErr,
			Meta:              json.RawMessage(`{"trace":"t"}`),
		}
		converted, err := mcpext.ConvertMcpResult("docs", "search", res, mcpext.ConvertMcpResultOptions{})
		if err != nil {
			t.Fatalf("ConvertMcpResult: %v", err)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(converted.StructuredContent, &obj); err != nil {
			t.Fatalf("unmarshal structuredContent: %v", err)
		}
		if _, hasMeta := obj["_meta"]; hasMeta {
			t.Fatalf("structuredContent kept _meta: %s", converted.StructuredContent)
		}
		if got := string(obj["content"]); got != "[]" {
			t.Fatalf("structuredContent.content = %s, want [] (full=%s)", got, converted.StructuredContent)
		}
	})

	t.Run("resource_link fractional and large sizes format like JS formatSize without int truncation", func(t *testing.T) {
		for _, tc := range []struct {
			size float64
			want string
		}{
			{512.5, `[Resource docs://guide "guide" (text/markdown, 512.5B)]`},
			{1280, `[Resource docs://guide "guide" (text/markdown, 1.3KB)]`},
			{1e19, `[Resource docs://guide "guide" (text/markdown, 9536743164062.5MB)]`},
		} {
			size := tc.size
			res := &mcp.CallToolResult{
				Content: []mcp.ContentBlock{{
					Type:     mcp.ContentResourceLink,
					URI:      "docs://guide",
					Name:     "guide",
					MimeType: "text/markdown",
					Size:     &size,
				}},
			}
			converted, err := mcpext.ConvertMcpResult("docs", "get", res, mcpext.ConvertMcpResultOptions{})
			if err != nil {
				t.Fatalf("ConvertMcpResult(%v): %v", tc.size, err)
			}
			if len(converted.Content) != 1 {
				t.Fatalf("size %v: len(content) = %d, want 1", tc.size, len(converted.Content))
			}
			if got := converted.Content[0].(ai.TextContent).Text; got != tc.want {
				t.Fatalf("size %v: resource_link text = %q, want %q", tc.size, got, tc.want)
			}
		}
	})
}
