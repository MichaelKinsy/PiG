package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBulkSession writes a session file of the shape a long coding session has: a header, then turns of a user prompt, an assistant message with thinking, text and a tool call, and a tool result whose text is source code with escaped newlines and quotes.
func writeBulkSession(tb testing.TB, turns int) (path string, size int64) {
	tb.Helper()
	code := strings.Repeat("func main() {\n\tfmt.Println(\"hello, \\\"world\\\"\")\n}\n// héllo 日本語\n", 300)
	var b strings.Builder
	b.WriteString(`{"type":"session","version":3,"id":"bulk","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}` + "\n")
	parent := "null"
	id := func(n int) string { return fmt.Sprintf("%08x", n) }
	for turn := range turns {
		user, assistant, result := id(turn*3+1), id(turn*3+2), id(turn*3+3)
		fmt.Fprintf(&b, `{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"please look at file %d","timestamp":1767225600000}}`+"\n", user, parent, turn)
		fmt.Fprintf(&b, `{"type":"message","id":%q,"parentId":%q,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":%q},{"type":"text","text":"Reading it."},{"type":"toolCall","id":"c%d","name":"read","arguments":{"path":"/w/f%d.go"}}],"api":"anthropic-messages","provider":"anthropic","model":"m","usage":{"input":1200,"output":340,"cacheRead":90000,"cacheWrite":0,"totalTokens":91540,"cost":{"input":0.0036,"output":0.0051,"cacheRead":0.027,"cacheWrite":0,"total":0.0357}},"stopReason":"toolUse","timestamp":1767225601000}}`+"\n", assistant, user, strings.Repeat("reasoning about the change ", 40), turn, turn)
		fmt.Fprintf(&b, `{"type":"message","id":%q,"parentId":%q,"timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"toolResult","toolCallId":"c%d","toolName":"read","content":[{"type":"text","text":%q}],"isError":false,"timestamp":1767225602000}}`+"\n", result, assistant, turn, code)
		parent = fmt.Sprintf("%q", result)
	}
	path = filepath.Join(tb.TempDir(), "bulk.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		tb.Fatal(err)
	}
	return path, int64(b.Len())
}

// BenchmarkSessionOpen opens a 100 MB-class session the way resume does: every line is validated and indexed once.
func BenchmarkSessionOpen(b *testing.B) {
	path, size := writeBulkSession(b, 900)
	dir := filepath.Dir(path)
	b.SetBytes(size)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewSessionManagerWithDir(dir, dir).Open(path); err != nil {
			b.Fatal(err)
		}
	}
}
