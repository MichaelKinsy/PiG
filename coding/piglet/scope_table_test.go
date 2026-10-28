package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The docs carry the tool scope rule table, which is computed from the code (scope_table.go). Run the test with
// PIG_UPDATE_TOOL_SCOPE_DOCS=1 to rewrite the table in both files.
func TestToolScopeTableMatchesDocs(t *testing.T) {
	want := ToolScopeTableMarkdown()
	for _, name := range []string{"piglets.md", "mcp.md"} {
		path := filepath.Join("..", "..", "internal", "pigdocs", "content", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		start, end := strings.Index(text, ToolScopeTableStart), strings.Index(text, ToolScopeTableEnd)
		if start < 0 || end < start {
			t.Fatalf("%s has no tool scope table between %q and %q", name, ToolScopeTableStart, ToolScopeTableEnd)
		}
		got := text[start : end+len(ToolScopeTableEnd)]
		if got == want {
			continue
		}
		if os.Getenv("PIG_UPDATE_TOOL_SCOPE_DOCS") == "1" {
			if err := os.WriteFile(path, []byte(text[:start]+want+text[end+len(ToolScopeTableEnd):]), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Errorf("%s: the tool scope table differs from the code; run PIG_UPDATE_TOOL_SCOPE_DOCS=1 go test ./coding/piglet -run TestToolScopeTableMatchesDocs\ngot:\n%s\nwant:\n%s", name, got, want)
	}
}

// The table's rows say what the scope rules do; this pins the rows that matter most so a rule change is a visible edit.
func TestToolScopeTableRows(t *testing.T) {
	table := ToolScopeTableMarkdown()
	for _, row := range []string{
		"| Piglet with no `extensions:` section | kept | kept | kept |",
		"| Piglet with `extensions:` but without `pig-mcp-adapter` | removed | removed | kept |",
		"| Piglet with `pig-mcp-adapter` and `tools: [mcp__docs__a]` | only the tools it names | removed | kept |",
		"| Stock `--tools read` | kept | kept | removed |",
		"| Stock `--no-tools` | removed | removed | removed |",
	} {
		if !strings.Contains(table, row) {
			t.Errorf("table lacks row %q:\n%s", row, table)
		}
	}
}
