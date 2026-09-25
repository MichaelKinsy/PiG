package widthx

import (
	"strings"
	"testing"
)

// A long mixed-language transcript exercises the shared layout primitives on
// repeated tool output, styles, wide characters, emoji, and ordinary prose.
func BenchmarkReviewTranscript(b *testing.B) {
	for _, tc := range []struct {
		name string
		line string
	}{
		{"ASCII", "assistant: inspect src/main.go and explain the result of this tool call. "},
		{"Styled", "\x1b[32massistant:\x1b[0m inspect src/main.go and explain the result. "},
		{"Unicode", "assistant: 日本語 한국어 क्ष กำ 👨‍👩‍👧‍👦 👍🏽 result of the tool call. "},
	} {
		text := strings.Repeat(tc.line+"\n", 1024)
		b.Run(tc.name+"/VisibleWidth", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				_ = VisibleWidth(text)
			}
		})
		b.Run(tc.name+"/Wrap", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				_ = WrapTextWithAnsi(text, 40)
			}
		})
	}
}
