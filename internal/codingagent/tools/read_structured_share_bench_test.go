package tools

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// BenchmarkReadResultEncodings is what one 20 KB file's read result costs in escaping: the tool's structured content, then the session entry
// that records the text. A new copy of the text each time, as each read makes, so neither finds an encoding made for an earlier one.
func BenchmarkReadResultEncodings(b *testing.B) {
	source := strings.Repeat("func (c *cache) get(key string) (value []byte, ok bool) {\n\t// \"quoted\" <tag> & more\n\treturn nil, false\n}\n", 180)
	b.SetBytes(int64(len(source)))
	b.ReportAllocs()
	var sink []byte
	for i := 0; b.Loop(); i++ {
		text := strings.Clone(source)
		content := []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
		structured := readStructuredContent(content)
		entry, err := ai.TextContent{Text: text}.AppendStringify(sink[:0])
		if err != nil {
			b.Fatal(err)
		}
		sink = entry
		_ = structured
	}
}
