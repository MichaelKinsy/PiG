package tui

import "testing"

// pi: packages/tui/src/components/editor.ts wordWrapLine. Pi re-wraps a one-grapheme segment wider than maxWidth with the same input and exhausts the stack; PiG keeps it as one chunk (D159).
func TestEditorWrapsOverWideGraphemeWithoutRecursion(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		width      int
	}{
		{"tab at width 2", "a\tb", 2},
		{"tab alone at width 1", "\t", 1},
		{"double-width character at width 1", "日本", 1},
		{"emoji at width 1", "x😀y", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks := wordWrapLine(tc.line, tc.width, nil)
			joined, next := "", 0
			for _, chunk := range chunks {
				if chunk.startIndex != next {
					t.Fatalf("chunk starts at %d, want %d: %+v", chunk.startIndex, next, chunks)
				}
				next = chunk.endIndex
				joined += chunk.text
			}
			if joined != tc.line {
				t.Fatalf("chunks %+v rejoin to %q, want %q", chunks, joined, tc.line)
			}
		})
	}
}
