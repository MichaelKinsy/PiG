//go:build !pig_strip_export_html

package codingagent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestRegression10285RendersThemInHTMLExportsToo(t *testing.T) {
	tui.SetTheme("dark")
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-10-03T12:00:00Z","cwd":"` + filepath.ToSlash(dir) + `"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-10-03T12:00:01Z","message":{"role":"user","content":"search","timestamp":1}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-03T12:00:02Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"mcp__my_docs__search","arguments":{"query":"pi"}}],"api":"anthropic-messages","provider":"anthropic","model":"test","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2}}
`
	if err := os.WriteFile(sessionPath, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ExportSessionToHTML(sessionPath, filepath.Join(dir, "export.html"), ExportToolRenderers(mcpRendererRunner(t)), dir, ShareState{}, "")
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(out)
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
	if got := stripANSITest(session.RenderedTools["call-1"].CallHTML); !strings.Contains(got, "my_docs/search") {
		t.Fatalf("callHtml = %q", got)
	}
}
