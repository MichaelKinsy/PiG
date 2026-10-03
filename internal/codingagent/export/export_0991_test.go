package export

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The viewer additions of upstream 0.99.1: hidden custom messages with their H toggle and the nested calls of a tool
// result (.upstream/v0.99.1/packages/coding-agent/src/core/export-html/template.js and template.css). Upstream has no
// test for them; the probe runs the exported page's own rendering functions.
func TestExportHTMLHiddenMessagesAndNestedCalls(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "session.jsonl")
	jsonl := `{"type":"session","version":3,"id":"test","cwd":"/tmp"}
{"type":"message","id":"a1","parentId":null,"message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"codemode","arguments":{"code":"x"}}]}}
{"type":"message","id":"r1","parentId":"a1","message":{"role":"toolResult","toolCallId":"c1","toolName":"codemode","content":[{"type":"text","text":"done"}],"isError":false,"nestedCalls":{"complete":true,"calls":[{"id":"c1/1","name":"read","arguments":{"path":"a"},"status":"ok","durationMs":5},{"id":"c1/2","name":"bash","argumentsBytes":9000,"status":"error","error":"boom\nline2"},{"id":"c1/3","name":"slow","arguments":{},"status":"unfinished"}]}}}
{"type":"custom_message","id":"m1","parentId":"r1","customType":"note","content":"secret","display":false}
{"type":"custom_message","id":"m2","parentId":"m1","customType":"note","content":"visible","display":true}
{"type":"message","id":"a2","parentId":"m2","message":{"role":"assistant","content":[{"type":"toolCall","id":"c2","name":"codemode","arguments":{"code":"y"}}]}}
{"type":"message","id":"r2","parentId":"a2","message":{"role":"toolResult","toolCallId":"c2","toolName":"codemode","content":[{"type":"text","text":"ok"}],"isError":false,"nestedCalls":{"complete":false,"calls":[{"id":"c2/1","name":"read","arguments":{},"status":"ok"}]}}}
{"type":"message","id":"a3","parentId":"r2","message":{"role":"assistant","content":[{"type":"toolCall","id":"c3","name":"read","arguments":{"path":"b"}}]}}
{"type":"message","id":"r3","parentId":"a3","message":{"role":"toolResult","toolCallId":"c3","toolName":"read","content":[{"type":"text","text":"plain"}],"isError":false}}
`
	if err := os.WriteFile(input, []byte(jsonl), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := ExportFromFile(input, filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/render_hidden_nested.cjs", output)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("export rendering: %v\n%s", err, combined)
	}
}
