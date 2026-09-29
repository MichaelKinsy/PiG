package mermaid

import (
	"reflect"
	"testing"
)

// grok-mermaid 0.2.3 src/parse.ts:278-289,472,576-593,660 discards style classes without swallowing the adjacent link or a member body.
func TestStyleClassAssignments(t *testing.T) {
	for _, tc := range []struct{ name, styled, plain string }{
		{"shaped flowchart", "flowchart LR\nA[Foo]:::highlight --> B[Bar]", "flowchart LR\nA[Foo] --> B[Bar]"},
		{"unspaced link", "flowchart LR\nA:::x-->B:::y", "flowchart LR\nA-->B"},
		{"hyphenated class", "flowchart LR\nA:::foo-bar-->B", "flowchart LR\nA-->B"},
		{"state", "stateDiagram-v2\nA:::hot-->B:::cold: move", "stateDiagram-v2\nA-->B: move"},
		{"class", "classDiagram\nclass Foo:::hot\nFoo:::hot --> Bar:::cold", "classDiagram\nclass Foo\nFoo --> Bar"},
		{"member untouched", "classDiagram\nclass Foo:::hot {\n+value:::literal\n}", "classDiagram\nclass Foo {\n+value:::literal\n}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, ok := Render(tc.plain)
			if !ok {
				t.Fatal("plain fixture must render")
			}
			got, ok := Render(tc.styled)
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("styled=%+v ok=%v; plain=%+v", got, ok, want)
			}
		})
	}
}

func BenchmarkStyleClassAssignments(b *testing.B) {
	for _, tc := range []struct{ name, source string }{
		{"flowchart", "flowchart LR\nA[Foo]:::highlight --> B[Bar]"},
		{"state", "stateDiagram-v2\nA:::hot-->B:::cold: move"},
		{"class", "classDiagram\nclass Foo:::hot\nFoo:::hot --> Bar:::cold"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, ok := Render(tc.source); !ok {
					b.Fatal("fixture did not render")
				}
			}
		})
	}
}
