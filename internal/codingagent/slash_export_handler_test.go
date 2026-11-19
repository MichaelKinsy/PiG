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

	iexport "github.com/MichaelKinsy/PiG/internal/codingagent/export"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

type exportTestComponent struct{ lines []string }

func (c exportTestComponent) Render(width int) []string { return c.lines }
func (exportTestComponent) Invalidate()                 {}

// The /export handler writes the HTML of a session with the custom tools pre-rendered; a Piglet Binary built with pig_strip_export_html
// (D92) has no HTML export, so this test needs the build to keep it.
func TestExportHandler_PrerendersCustomToolHTML(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	cwd, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	jsonl := `{"type":"session","id":"sess-1","cwd":` + string(cwd) + `,"timestamp":"2026-05-14T12:00:00Z"}
{"type":"message","id":"a1","timestamp":"2026-05-14T12:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"custom-tool","arguments":{"path":"README.md"}}]}}
{"type":"message","id":"r1","timestamp":"2026-05-14T12:00:02Z","message":{"role":"toolResult","toolCallId":"call-1","toolName":"custom-tool","content":[{"type":"text","text":"done"}],"isError":false}}
`
	if err := os.WriteFile(sessionPath, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "out.html")
	session := &Session{header: SessionHeader{CWD: dir}, path: sessionPath}
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	sc.Args = outPath
	sc.ToolRenderers = func() func(string) *extension.ToolRenderers {
		return ExportToolRenderers(inproc.NewRunner([]extension.Extension{{Name: "test-ext", Tools: map[string]extension.RegisteredTool{"custom-tool": {
			Definition: extension.ToolDefinition{
				Name: "custom-tool",
				RenderCall: func(args json.RawMessage, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
					return exportTestComponent{lines: []string{"\x1b[31mCALL\x1b[0m"}}
				},
				RenderResult: func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
					toolResult := result
					if options.Expanded {
						return exportTestComponent{lines: []string{"\x1b[32mRESULT: " + toolResult.Text() + "\x1b[0m"}}
					}
					return exportTestComponent{lines: []string{"preview"}}
				},
			},
		}}}}, dir))
	}
	if err := exportHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), outPath) {
		t.Fatalf("export confirmation missing output path: %q", out.String())
	}
	htmlBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlBytes)
	re := regexp.MustCompile(`<script id="session-data" type="application/json">([^<]+)</script>`)
	m := re.FindStringSubmatch(html)
	if len(m) != 2 {
		t.Fatal("session-data script not found")
	}
	payload, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatal(err)
	}
	var sd iexport.SessionData
	if err := json.Unmarshal(payload, &sd); err != nil {
		t.Fatal(err)
	}
	if sd.RenderedTools == nil || sd.RenderedTools["call-1"] == nil {
		t.Fatalf("RenderedTools missing custom tool html: %#v", sd.RenderedTools)
	}
	if got, _ := sd.RenderedTools["call-1"]["callHtml"].(string); !strings.Contains(got, "CALL") {
		t.Fatalf("callHtml = %q, want CALL", got)
	}
	if got, _ := sd.RenderedTools["call-1"]["resultHtmlExpanded"].(string); !strings.Contains(got, "RESULT: done") {
		t.Fatalf("resultHtmlExpanded = %q, want RESULT: done", got)
	}
}
