package mcpext_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/coding-agent/test/mcp-extension.test.ts ("MCP tools").

func TestMCPToolsCreatesProviderSafeToolNames(t *testing.T) {
	if got := mcpext.CreateMcpToolName("docs", "search", nil); got != "mcp__docs__search" {
		t.Fatalf("got %s", got)
	}
	if got := mcpext.CreateMcpToolName("my-server", "get.item/v2", nil); got != "mcp__my_server__get_item_v2" {
		t.Fatalf("got %s", got)
	}
	long := mcpext.CreateMcpToolName("server", strings.Repeat("x", 100), nil)
	if len(long) != 64 {
		t.Fatalf("long name has %d chars", len(long))
	}
	if !regexp.MustCompile(`^mcp__server__x+_[0-9a-f]{8}$`).MatchString(long) {
		t.Fatalf("long name = %s", long)
	}
	if other := mcpext.CreateMcpToolName("server", strings.Repeat("x", 100)+"y", nil); other == long {
		t.Fatal("different tools got the same shortened name")
	}
	// Names that sanitize to one already taken by another tool get a hash suffix.
	taken := mcpext.CreateMcpToolName("s", "a_b", nil)
	second := mcpext.CreateMcpToolName("s", "a-b", func(name string) bool { return name == taken })
	if !regexp.MustCompile(`^mcp__s__a_b_[0-9a-f]{8}$`).MatchString(second) {
		t.Fatalf("second = %s", second)
	}
}

func decodeResult(t *testing.T, raw string) *mcp.CallToolResult {
	t.Helper()
	var result mcp.CallToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func TestMCPToolsConvertsResultsPassingTheCallToolResultToScriptsAndFlaggingErrors(t *testing.T) {
	blocks := `[
		{"type":"resource_link","uri":"file:///a","name":"a"},
		{"type":"resource","resource":{"uri":"file:///b","text":"b text"}},
		{"type":"audio","data":"","mimeType":"audio/wav"}
	]`
	converted, err := mcpext.ConvertMcpResult("docs", "t", decodeResult(t, fmt.Sprintf(`{"content":%s,"structuredContent":{"ok":true},"_meta":{"trace":"x"}}`, blocks)), mcpext.ConvertMcpResultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, converted.Content, `[
		{"type":"text","text":"[Resource file:///a \"a\"]"},
		{"type":"text","text":"b text"},
		{"type":"text","text":"[audio audio/wav omitted]"}
	]`)
	jsonEqual(t, converted.Details, `{"server":"docs","tool":"t"}`)
	// Scripts get the server's blocks as sent, without `_meta`.
	jsonEqual(t, json.RawMessage(converted.StructuredContent), fmt.Sprintf(`{"content":%s,"structuredContent":{"ok":true}}`, blocks))
	if converted.IsError {
		t.Fatal("result flagged as an error")
	}

	fallback, err := mcpext.ConvertMcpResult("docs", "t", decodeResult(t, `{"content":[],"structuredContent":{"n":1}}`), mcpext.ConvertMcpResultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, fallback.Content, `[{"type":"text","text":"{\n  \"n\": 1\n}"}]`)

	failure, err := mcpext.ConvertMcpResult("docs", "t", decodeResult(t, `{"content":[{"type":"text","text":"nope"}],"isError":true}`), mcpext.ConvertMcpResultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, failure.Content, `[{"type":"text","text":"nope"}]`)
	jsonEqual(t, failure.Details, `{"server":"docs","tool":"t"}`)
	jsonEqual(t, json.RawMessage(failure.StructuredContent), `{"content":[{"type":"text","text":"nope"}],"isError":true}`)
	if !failure.IsError {
		t.Fatal("isError result not flagged")
	}

	empty, err := mcpext.ConvertMcpResult("docs", "t", decodeResult(t, `{"content":[],"isError":true}`), mcpext.ConvertMcpResultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, empty.Content, `[{"type":"text","text":"MCP tool docs/t returned an error"}]`)
}

func TestMCPToolsPointsResourceLinksToReadMCPResourceAndSavesBinaryResources(t *testing.T) {
	type savedOutput struct {
		data      string
		extension string
	}
	var saved []savedOutput
	saveOutput := func(data []byte, extension string) (string, error) {
		saved = append(saved, savedOutput{string(data), extension})
		return "/tmp/saved" + extension, nil
	}
	converted, err := mcpext.ConvertMcpResult("docs", "t", decodeResult(t, `{"content":[
		{"type":"resource_link","uri":"docs://guide","name":"guide","title":"The Guide","mimeType":"text/markdown","size":2048,"description":"How to use it"},
		{"type":"resource","resource":{"uri":"file:///r/report.pdf","mimeType":"application/pdf","blob":"JVBERg=="}},
		{"type":"resource","resource":{"uri":"docs://logo","mimeType":"image/png","blob":"AAAA"}}
	]}`), mcpext.ConvertMcpResultOptions{SaveOutput: saveOutput, ReadableResources: true})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, converted.Content, `[
		{"type":"text","text":"[Resource docs://guide \"The Guide\" (text/markdown, 2.0KB): How to use it. Read it with read_mcp_resource (server \"docs\")]"},
		{"type":"text","text":"[Binary resource file:///r/report.pdf (application/pdf, 4B) saved to /tmp/saved.pdf]"},
		{"type":"image","data":"AAAA","mimeType":"image/png"}
	]`)
	if len(saved) != 1 || saved[0] != (savedOutput{"%PDF", ".pdf"}) {
		t.Fatalf("saved = %#v", saved)
	}
}

func TestMCPToolsCutsTheMiddleOfModelFacingTextOver20KBAndKeepsTheFullResultForScripts(t *testing.T) {
	var saved []string
	saveOutput := func(data []byte, _ string) (string, error) {
		saved = append(saved, string(data))
		return "/tmp/full.txt", nil
	}
	lines := make([]string, 3000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	full := strings.Join(lines, "\n")
	image := `{"type":"image","data":"AAAA","mimeType":"image/png"}`
	fullJSON, _ := json.Marshal(full)
	resultJSON := fmt.Sprintf(`{"content":[{"type":"text","text":%s},%s]}`, fullJSON, image)
	converted, err := mcpext.ConvertMcpResult("docs", "snapshot", decodeResult(t, resultJSON), mcpext.ConvertMcpResultOptions{SaveOutput: saveOutput})
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Content) != 2 {
		t.Fatalf("content has %d blocks", len(converted.Content))
	}
	text := converted.Content[0].(ai.TextContent).Text
	// Codex's format: a header, the start and end of the text, then the file with the full text.
	header := fmt.Sprintf("^Warning: truncated output \\(original token count: %d\\)\nTotal output lines: 3000\n\nline 1\nline 2\n", int(math.Ceil(float64(len(full))/4)))
	if !regexp.MustCompile(header).MatchString(text) {
		t.Fatalf("text header = %q", text[:200])
	}
	if !regexp.MustCompile(`…\d+ chars truncated…`).MatchString(text) {
		t.Fatal("no truncation marker")
	}
	if !strings.HasSuffix(text, "line 3000\n\n[Full output: /tmp/full.txt (read it with offset/limit)]") {
		t.Fatalf("text tail = %q", text[len(text)-80:])
	}
	if len(text) >= 21*1024 {
		t.Fatalf("text has %d bytes", len(text))
	}
	jsonEqual(t, converted.Content[1], image)
	jsonEqual(t, converted.Details, `{"server":"docs","tool":"snapshot","fullOutputPath":"/tmp/full.txt"}`)
	if len(saved) != 1 || saved[0] != full {
		t.Fatalf("saved %d outputs", len(saved))
	}
	jsonEqual(t, json.RawMessage(converted.StructuredContent), resultJSON)

	// Text within the limit is not saved.
	if _, err := mcpext.ConvertMcpResult("docs", "small", decodeResult(t, `{"content":[{"type":"text","text":"ok"}]}`), mcpext.ConvertMcpResultOptions{SaveOutput: saveOutput}); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 {
		t.Fatalf("saved %d outputs", len(saved))
	}
}

// Results can carry private data, so only the user may read the saved file.
func TestMCPToolsSavesOutputInAUserOnlyTempFile(t *testing.T) {
	path, err := mcpext.SaveToTempFile([]byte("secret"), ".txt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if !regexp.MustCompile(`^pi-mcp-[0-9a-f]{16}\.txt$`).MatchString(filepath.Base(path)) {
		t.Errorf("path = %q", path)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "secret" {
		t.Errorf("content = %q, %v", data, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got&0o077 != 0 {
		t.Errorf("mode = %o, want no access for group and others", got)
	}
}
