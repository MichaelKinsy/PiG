//go:build !pig_strip_export_html

// pi: packages/coding-agent/src/core/export-html/tool-renderer.ts
// pi: packages/coding-agent/src/core/export-html/index.ts

package codingagent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// write10285Session writes the session of 10285-mcp-tool-renderers.test.ts "renders them in HTML exports too": a user
// message and an assistant message calling the MCP tool mcp__my_docs__search, which no extension registered.
func write10285Session(t *testing.T, dir string) string {
	t.Helper()
	sessionPath := filepath.Join(dir, "session.jsonl")
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-10-03T12:00:00Z","cwd":"` + filepath.ToSlash(dir) + `"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-10-03T12:00:01Z","message":{"role":"user","content":"search","timestamp":1}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-03T12:00:02Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"mcp__my_docs__search","arguments":{"query":"pi"}}],"api":"anthropic-messages","provider":"anthropic","model":"test","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2}}
`
	if err := os.WriteFile(sessionPath, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	return sessionPath
}

// callHTMLOf reads session.renderedTools["call-1"].callHtml out of an exported page's session-data payload.
func callHTMLOf(t *testing.T, exportPath string) string {
	t.Helper()
	html, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`<script id="session-data" type="application/json">([^<]*)</script>`).FindSubmatch(html)
	if match == nil {
		t.Fatal("session-data script not found")
	}
	data, err := base64.StdEncoding.DecodeString(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		RenderedTools map[string]struct {
			CallHTML string `json:"callHtml"`
		} `json:"renderedTools"`
	}
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	return session.RenderedTools["call-1"].CallHTML
}

func TestRegression10285RendersThemInHTMLExportsToo(t *testing.T) {
	tui.SetTheme("dark")
	dir := t.TempDir()
	sessionPath := write10285Session(t, dir)
	out, err := ExportSessionToHTML(sessionPath, filepath.Join(dir, "export.html"), ExportToolRenderers(mcpRendererRunner(t)), dir, ShareState{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := stripANSITest(callHTMLOf(t, out)); !strings.Contains(got, "my_docs/search") {
		t.Fatalf("callHtml = %q", got)
	}
}

// The same export through /export, which is the Session's exportToHtml of the Pi test: the handler hands the host's
// tool renderers to the exporter, so the MCP call that no extension registered is still drawn by the MCP renderer.
func TestRegression10285SlashExportRendersThemInHTMLExports(t *testing.T) {
	tui.SetTheme("dark")
	dir := t.TempDir()
	session := &Session{header: SessionHeader{CWD: dir}, path: write10285Session(t, dir)}
	runner := mcpRendererRunner(t)
	sc, _ := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	sc.ToolRenderers = func() func(string) *extension.ToolRenderers { return ExportToolRenderers(runner) }
	outPath := filepath.Join(dir, "export.html")
	sc.Args = outPath
	if err := exportHandler(sc); err != nil {
		t.Fatal(err)
	}
	if got := stripANSITest(callHTMLOf(t, outPath)); !strings.Contains(got, "my_docs/search") {
		t.Fatalf("callHtml = %q", got)
	}
}
