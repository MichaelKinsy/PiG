package tui

import (
	"strings"
	"testing"
)

func BenchmarkInputWordEditing(b *testing.B) {
	for _, tc := range []struct{ name, text string }{{"ASCII", strings.Repeat("hello world. ", 64)}, {"CJK", strings.Repeat("你好世界。你好，世界 ", 64)}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.text)))
			for b.Loop() {
				input := NewInput(InputOptions{})
				input.SetValue(tc.text)
				input.HandleInput("\x05")
				input.HandleInput("\x17")
				input.HandleInput("\x19")
				input.Render(80)
				if input.GetValue() != tc.text {
					b.Fatal("kill/yank changed text")
				}
			}
		})
	}
}
