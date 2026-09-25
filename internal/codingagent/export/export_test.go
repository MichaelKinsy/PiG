package export

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type testComponent struct{ lines []string }

func (c testComponent) Render(width int) []string { return c.lines }

// mustRaw marshals v to json.RawMessage. Panics on error.
func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func extractSessionDataBase64(t *testing.T, html string) string {
	t.Helper()
	re := regexp.MustCompile(`<script id="session-data" type="application/json">([^<]+)</script>`)
	m := re.FindStringSubmatch(html)
	if len(m) != 2 {
		t.Fatalf("session-data script not found")
	}
	return m[1]
}

// decodeSessionDataMap decodes the base64 session-data from HTML into a
// generic map for assertions that need to inspect key values without
// worrying about json.RawMessage.
func decodeSessionDataMap(t *testing.T, html string) map[string]any {
	t.Helper()
	b64 := extractSessionDataBase64(t, html)
	payload, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	return m
}

func TestToHTML_EmbedsSPAAssetsAndPayload(t *testing.T) {
	sd := SessionData{
		Header: mustRaw(map[string]any{"type": "session", "id": "test-123", "cwd": "/tmp/test"}),
		Entries: []json.RawMessage{
			mustRaw(map[string]any{"type": "message", "id": "u1", "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": "Hello world"}}}}),
		},
	}
	html := ToHTML(sd)
	for _, want := range []string{
		"<!DOCTYPE html>",
		"id=\"hamburger\"",
		"id=\"tree-search\"",
		"filter-btn active",
		"marked.parse",
		"hljs.highlight(",
		"const { header, entries, leafId: defaultLeafId, systemPrompt, tools, renderedTools } = data;",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q", want)
		}
	}
	got := decodeSessionDataMap(t, html)
	header, _ := got["header"].(map[string]any)
	if header["id"] != "test-123" {
		t.Fatalf("header.id = %#v", header["id"])
	}
	entries, _ := got["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
}

func TestFromJSONL_PreservesRawEntriesAndLeaf(t *testing.T) {
	jsonl := `{"type":"session","id":"abc","cwd":"/tmp","timestamp":"2026-05-10T12:00:00Z"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-05-10T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"label","id":"l1","parentId":"u1","timestamp":"2026-05-10T12:00:02Z","targetId":"u1","label":"start"}
`
	sd, err := FromJSONL([]byte(jsonl))
	if err != nil {
		t.Fatalf("FromJSONL: %v", err)
	}
	// Unmarshal header to check values.
	var header map[string]any
	if err := json.Unmarshal(sd.Header, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if header["id"] != "abc" {
		t.Fatalf("header.id = %#v", header["id"])
	}
	if len(sd.Entries) != 2 {
		t.Fatalf("len(entries) = %d", len(sd.Entries))
	}
	if sd.LeafID == nil || *sd.LeafID != "l1" {
		t.Fatalf("leafId = %#v", sd.LeafID)
	}
	// Check entry[1] type.
	var entry1 map[string]any
	if err := json.Unmarshal(sd.Entries[1], &entry1); err != nil {
		t.Fatalf("unmarshal entry[1]: %v", err)
	}
	if entry1["type"] != "label" {
		t.Fatalf("entry[1].type = %#v", entry1["type"])
	}
}

func TestFromJSONL_PreservesKeyOrder(t *testing.T) {
	// Verify that the original key ordering from the JSONL is preserved
	// in the raw header bytes (not sorted alphabetically).
	jsonl := `{"type":"session","id":"test","cwd":"/tmp"}
{"type":"message","id":"u1","parentId":null}
`
	sd, err := FromJSONL([]byte(jsonl))
	if err != nil {
		t.Fatalf("FromJSONL: %v", err)
	}
	// The raw bytes should start with {"type": (original order)
	// not {"cwd": (alphabetical).
	headerStr := string(sd.Header)
	if !strings.HasPrefix(headerStr, `{"type":"session"`) {
		t.Fatalf("header key order not preserved: %s", headerStr)
	}
	entryStr := string(sd.Entries[0])
	if !strings.HasPrefix(entryStr, `{"type":"message"`) {
		t.Fatalf("entry key order not preserved: %s", entryStr)
	}
}

func TestExportFromFile_WritesSPAHTML(t *testing.T) {
	dir := t.TempDir()
	// The default output lands in the working directory. Keep it out of the
	// source tree, where concurrent snapshot tests (git add -A) would see it.
	t.Chdir(dir)
	input := dir + "/session.jsonl"
	jsonl := `{"type":"session","id":"test","cwd":"/tmp","timestamp":"2026-05-10T12:00:00Z"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-05-10T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
`
	if err := os.WriteFile(input, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ExportFromFile(input, "")
	if err != nil {
		t.Fatalf("ExportFromFile: %v", err)
	}
	if !strings.Contains(out, "pig-session-session.html") {
		t.Fatalf("out = %q", out)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	if !strings.Contains(html, "id=\"messages\"") {
		t.Fatalf("messages container missing")
	}
	decoded := decodeSessionDataMap(t, html)
	header, _ := decoded["header"].(map[string]any)
	if header["id"] != "test" {
		t.Fatalf("decoded header id = %#v", header["id"])
	}
	_ = os.Remove(out)
}

func TestExportHTML_DoesNotInlineRawScriptTag(t *testing.T) {
	sd := SessionData{
		Header: mustRaw(map[string]any{"type": "session", "id": "xss", "cwd": "/tmp"}),
		Entries: []json.RawMessage{mustRaw(map[string]any{
			"type": "message",
			"id":   "u1",
			"message": map[string]any{
				"role":    "user",
				"content": []map[string]any{{"type": "text", "text": `<script>alert("xss")</script>`}},
			},
		})},
	}
	html := ToHTML(sd)
	if strings.Contains(html, `<script>alert("xss")</script>`) {
		t.Fatal("raw script tag leaked into HTML source")
	}
}

func TestExportHTML_WhitespaceCSSRulePresent(t *testing.T) {
	html := ToHTML(SessionData{Header: mustRaw(map[string]any{"type": "session", "id": "w", "cwd": "/tmp"})})
	if !strings.Contains(html, ".output-preview,") || !strings.Contains(html, ".output-full {") || !strings.Contains(html, "white-space: pre-wrap;") {
		t.Fatal("plain-text whitespace rule missing from CSS")
	}
}

func TestExportHTML_TemplateJSContainsXSSGuards(t *testing.T) {
	html := ToHTML(SessionData{Header: mustRaw(map[string]any{"type": "session", "id": "safe", "cwd": "/tmp"})})
	for _, want := range []string{"javascript:", "vbscript:", "escapeHtml(href)", "escapeHtml(img.mimeType"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing XSS guard marker %q", want)
		}
	}
}

func TestAnsiToHTML_ConvertsStylesAndEscapesHTML(t *testing.T) {
	got := ansiToHTML("\x1b[31;1m<hello>\x1b[0m")
	for _, want := range []string{`color:#800000`, `font-weight:bold`, `&lt;hello&gt;`} {
		if !strings.Contains(got, want) {
			t.Fatalf("ansiToHTML() missing %q in %q", want, got)
		}
	}
}

func TestTrimRenderedResultLines(t *testing.T) {
	lines := []string{"", "   ", "\x1b[2m  \x1b[0m", "keep", "\x1b[31mvalue\x1b[0m", "\x1b[2m \x1b[0m", "   "}
	got := trimRenderedResultLines(lines)
	want := []string{"keep", "\x1b[31mvalue\x1b[0m"}
	if len(got) != len(want) {
		t.Fatalf("trimRenderedResultLines len = %d, want %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trimRenderedResultLines[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRenderCustomTools_PrerendersExtensionToolHTML(t *testing.T) {
	sd := &SessionData{
		Header: mustRaw(map[string]any{"type": "session", "id": "test", "cwd": "/tmp"}),
		Entries: []json.RawMessage{
			mustRaw(map[string]any{
				"type": "message",
				"id":   "a1",
				"message": map[string]any{
					"role": "assistant",
					"content": []any{map[string]any{
						"type":      "toolCall",
						"id":        "call-1",
						"name":      "custom-tool",
						"arguments": map[string]any{"path": "README.md"},
					}},
				},
			}),
			mustRaw(map[string]any{
				"type": "message",
				"id":   "r1",
				"message": map[string]any{
					"role":       "toolResult",
					"toolCallId": "call-1",
					"toolName":   "custom-tool",
					"content":    []any{map[string]any{"type": "text", "text": "done"}},
					"isError":    false,
				},
			}),
		},
	}
	tools := []extension.RegisteredTool{{
		Definition: extension.ToolDefinition{
			Name: "custom-tool",
			RenderCall: func(args json.RawMessage, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
				return testComponent{lines: []string{"\x1b[31mCALL\x1b[0m"}}
			},
			RenderResult: func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
				toolResult := result.(agent.AgentToolResult)
				if options.Expanded {
					return testComponent{lines: []string{"", "\x1b[32mRESULT: " + toolResult.Content + "\x1b[0m", ""}}
				}
				return testComponent{lines: []string{"", "preview", ""}}
			},
		},
	}}

	RenderCustomTools(sd, tools, "/tmp", 80)
	if sd.RenderedTools == nil {
		t.Fatal("RenderedTools = nil, want pre-rendered tool HTML")
	}
	got := sd.RenderedTools["call-1"]
	if got == nil {
		t.Fatal("RenderedTools[call-1] missing")
	}
	for key, want := range map[string]string{
		"callHtml":            "CALL",
		"resultHtmlCollapsed": "preview",
		"resultHtmlExpanded":  "RESULT: done",
	} {
		value, _ := got[key].(string)
		if !strings.Contains(value, want) {
			t.Fatalf("RenderedTools[%q][%q] = %q, want substring %q", "call-1", key, value, want)
		}
	}
}

func TestJsReplace_DollarAmpersand(t *testing.T) {
	// $& in replacement should be replaced with the search string
	got := jsReplace("hello {{X}} world", "{{X}}", "before $& after")
	want := "hello before {{X}} after world"
	if got != want {
		t.Fatalf("jsReplace $& = %q, want %q", got, want)
	}
}

func TestJsReplace_DoubleDollar(t *testing.T) {
	// $$ in replacement should become literal $
	got := jsReplace("hello {{X}} world", "{{X}}", "cost $$5")
	want := "hello cost $5 world"
	if got != want {
		t.Fatalf("jsReplace $$ = %q, want %q", got, want)
	}
}

func TestJsReplace_NoDollar(t *testing.T) {
	// No $ patterns: literal replacement
	got := jsReplace("hello {{X}} world", "{{X}}", "foo")
	want := "hello foo world"
	if got != want {
		t.Fatalf("jsReplace no-dollar = %q, want %q", got, want)
	}
}

// TestGenerateThemeVars_IncludesScrollbarThumb guards that the HTML
// theme-variable surface (which iterates ColorKeys) emits the fullscreen
// scrollbar tokens: the thumb resolves like the text color (its upstream
// fallback) and the track resolves to its own color.
func TestGenerateThemeVars_IncludesScrollbarThumb(t *testing.T) {
	css := generateThemeVars()
	thumb := extractCSSVar(css, "scrollbarThumb")
	if thumb == "" {
		t.Fatalf("HTML theme vars omit --scrollbarThumb:\n%s", css)
	}
	if text := extractCSSVar(css, "text"); thumb != text {
		t.Fatalf("--scrollbarThumb = %q, want the text color %q", thumb, text)
	}
	if extractCSSVar(css, "scrollbarTrack") == "" {
		t.Fatalf("HTML theme vars omit --scrollbarTrack:\n%s", css)
	}
}

func extractCSSVar(css, name string) string {
	for line := range strings.SplitSeq(css, "\n") {
		prefix := "--" + name + ": "
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return strings.TrimSuffix(strings.TrimSpace(after), ";")
		}
	}
	return ""
}
