package export

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/export-html-skill-block.test.ts with the same source assertions.
func TestExportHTMLSkillBlock(t *testing.T) {
	js := mustReadAsset("assets/template.js")
	for _, tc := range []struct {
		name     string
		patterns []string
	}{
		{"strips skill wrapper XML from user message rendering", []string{`parseSkillBlock`, `skillBlock\.userMessage`}},
		{"renders skill invocation and user message as separate sibling blocks", []string{`skill-invocation`, `hasUserContent`}},
		{"renders skill content as markdown, not raw text", []string{`safeMarkedParse\(skillBlock\.content\)`}},
		{"shows skill name and user message in the sidebar tree", []string{`tree-role-skill`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, pattern := range tc.patterns {
				if !regexp.MustCompile(pattern).MatchString(js) {
					t.Errorf("missing %s", pattern)
				}
			}
		})
	}
}

// Ports packages/coding-agent/test/export-html-xss.test.ts with all nine cases and the same positive/negative assertions.
func TestExportHTMLMarkdownSanitization(t *testing.T) {
	js := mustReadAsset("assets/template.js")
	for _, tc := range []struct {
		name         string
		want, reject []string
	}{
		{"link scheme allow-list", []string{`link\s*\(\s*token\s*\)`, `sanitizeMarkdownUrl\(token\.href\)`, `\^\(https\?\|mailto\|tel\|ftp\)`}, nil},
		{"image scheme allow-list", []string{`image\s*\(\s*token\s*\)`, `sanitizeMarkdownUrl\(token\.href\)`}, nil},
		{"C0 controls", []string{regexp.QuoteMeta(`replace(/[\x00-\x1f\x7f]/g, '')`)}, []string{`(?i)\^\\s\*\(javascript\|vbscript\|data\):`}},
		{"href attributes", []string{`escapeHtml\(href\)`}, nil},
		{"image mimeType attributes", []string{`escapeHtml\(img\.mimeType`}, []string{`\$\{img\.mimeType\}`}},
		{"image data attributes", []string{`;base64,\$\{escapeHtml\(img\.data \|\| (?:''|"")\)\}"`}, []string{`;base64,\$\{img\.data\}"`}},
		{"entry IDs", []string{`entry-\$\{escapeHtml\(entry\.id\)\}`, `data-entry-id="\$\{escapeHtml\(entryId\)\}"`}, []string{`id="\$\{entryId\}"`, `data-entry-id="\$\{entryId\}"`}},
		{"tree metadata", []string{`\$\{escapeHtml\(msg\.toolName \|\| 'tool'\)\}`, `\$\{escapeHtml\(msg\.role\)\}`, `\$\{escapeHtml\(entry\.modelId\)\}`, `\$\{escapeHtml\(entry\.thinkingLevel\)\}`, `\$\{escapeHtml\(entry\.type\)\}`}, []string{`\[\$\{msg\.toolName \|\| 'tool'\}\]`, `\[\$\{msg\.role\}\]`, `\[model: \$\{entry\.modelId\}\]`, `\[thinking: \$\{entry\.thinkingLevel\}\]`, `\[\$\{entry\.type\}\]`}},
		{"header models", []string{`\$\{escapeHtml\(globalStats\.models\.join\(', '\) \|\| 'unknown'\)\}`}, []string{`\$\{globalStats\.models\.join\(', '\) \|\| 'unknown'\}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, pattern := range tc.want {
				if !regexp.MustCompile(pattern).MatchString(js) {
					t.Errorf("missing %s", pattern)
				}
			}
			for _, pattern := range tc.reject {
				if regexp.MustCompile(pattern).MatchString(js) {
					t.Errorf("unsafe pattern %s", pattern)
				}
			}
		})
	}
}

// Ports packages/coding-agent/test/export-html-whitespace.test.ts: ANSI line boundaries carry no source whitespace.
func TestAnsiLinesToHTMLNoSourceWhitespace(t *testing.T) {
	got := ansiLinesToHTML([]string{"one", "two"})
	want := `<div class="ansi-line">one</div><div class="ansi-line">two</div>`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Ports packages/coding-agent/test/export-html-whitespace.test.ts: trim the renderer's surrounding TUI spacing.
func TestCustomToolResultHTMLTrimsSpacing(t *testing.T) {
	tools := []extension.RegisteredTool{{Definition: extension.ToolDefinition{
		Name: "custom", Label: "custom", Description: "custom",
		RenderResult: func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
			return testComponent{lines: []string{"", "\x1b[31mone\x1b[0m", "two", ""}}
		},
	}}}
	renderer := newToolHTMLRenderer(tools, "/tmp", 100)
	got := renderer.renderResult("id", "custom", agent.AgentToolResult{})
	want := `<div class="ansi-line"><span style="color:#800000">one</span></div><div class="ansi-line">two</div>`
	if got.ResultHTMLExpanded != want || got.ResultHTMLCollapsed != "" {
		t.Fatalf("got %+v, want only expanded %q", got, want)
	}
}

// These are the reviewed Pi 0.87.1 source assets, not hashes of Pig's generated output.
// Refresh from .upstream/current/packages/coding-agent/src/core/export-html with sha256sum when the upstream pin changes.
func TestExportHTMLPinnedAssets(t *testing.T) {
	for name, want := range map[string]string{
		"template.html":           "916782b1184a9597527605ad751e2b3af30fcea23ba2194002969cd217a06881",
		"template.css":            "28c16e3827c23a62eef8283cac316b478f946e023093adad25ff9c9b891d41af",
		"template.js":             "56270347d35faac17e3b79b16ac5d8dbab664e9c83cfbc05437a3f5976074d76",
		"vendor/marked.min.js":    "d5487edc7258b404bfa74c393d74a6393155f02517bd5e7e77cd64f8187f39a0",
		"vendor/highlight.min.js": "837a6fa5b0c736b52bbde2b2b6190f305da3fc9ed41681db5321507057b5c846",
	} {
		t.Run(name, func(t *testing.T) {
			got := fmt.Sprintf("%x", sha256.Sum256([]byte(mustReadAsset("assets/"+name))))
			if got != want {
				t.Fatalf("asset differs from pinned Pi: got %s, want %s", got, want)
			}
		})
	}
}

func TestExportHTMLPayloadPreservesText(t *testing.T) {
	// index.ts:161 encodes JSON.stringify, not HTML-escaped JSON, inside base64.
	for _, text := range []string{"", "<skill> & </skill>", "line\u2028paragraph\u2029", `literal \u2028`} {
		header := `{"type":"session","version":3,"id":"test","cwd":"/tmp"}`
		sd, err := FromJSONL([]byte(header))
		if err != nil {
			t.Fatal(err)
		}
		sd.SystemPrompt = text
		html := ToHTML(sd)
		payload, err := base64.StdEncoding.DecodeString(extractSessionDataBase64(t, html))
		if err != nil {
			t.Fatal(err)
		}
		want := `{"header":` + header + `,"entries":[],"leafId":null`
		if text != "" {
			quoted := strings.ReplaceAll(text, `\`, `\\`)
			want += `,"systemPrompt":"` + quoted + `"`
		}
		want += `}`
		if string(payload) != want {
			t.Errorf("text %q: got %s, want %s", text, payload, want)
		}
	}
}

func TestExportHTMLRendering(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(input, []byte(`{"type":"session","version":3,"id":"test","cwd":"/tmp"}
{"type":"message","id":"u1","parentId":null,"message":{"role":"user","content":"ordinary"}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := ExportFromFile(input, filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/render.cjs", output)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("export rendering: %v\n%s", err, output)
	}
}

func BenchmarkToHTML(b *testing.B) {
	sd := SessionData{Header: json.RawMessage(`{"type":"session","version":3,"id":"bench","cwd":"/tmp"}`)}
	for i := range 1000 {
		sd.Entries = append(sd.Entries, json.RawMessage(fmt.Sprintf(`{"type":"message","id":"u%d","message":{"role":"user","content":"<skill name=\"review\" location=\"/skills/SKILL.md\">\nReview **code**.\n</skill>\n\nReview this change."}}`, i)))
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = ToHTML(sd)
	}
}

func TestJsReplaceOnlyFirstOccurrence(t *testing.T) {
	// packages/coding-agent/src/core/export-html/index.ts:164-177 uses non-global String.replace.
	for _, replacement := range []string{"literal", "$$ $& $` $'"} {
		got := jsReplace("before {{X}} after {{X}}", "{{X}}", replacement)
		want := "before literal after {{X}}"
		if strings.Contains(replacement, "$") {
			want = "before $ {{X}} before   after {{X}} after {{X}}"
		}
		if got != want {
			t.Errorf("replacement %q: got %q, want %q", replacement, got, want)
		}
	}
}
