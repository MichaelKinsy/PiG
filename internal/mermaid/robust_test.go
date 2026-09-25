package mermaid

import (
	"strings"
	"testing"
	"time"
)

// TestRenderRobustness feeds adversarial/malformed input and asserts Render
// never panics and always terminates. The engine runs on the TUI render loop,
// so a panic or infinite loop there would crash or freeze the UI.
//
// Deeply nested / huge inputs are bounded in OUTPUT (Render returns null once a
// cap is hit: MAX_GROUP_DEPTH, MAX_NODES, MAX_EDGES) but their parse/layout
// COST is superlinear on pathological input, exactly as upstream grok-mermaid
// (grok itself takes ~6s at 2000-deep, ~92s at 5000-deep; pig is faster). pi's
// transformer applies no input-size guard, so pig matches it; realistic diagrams
// (bounded by the caps) render in microseconds: see BenchmarkRender. The cases
// below stay within the realistic-pathological range that terminates quickly.
func TestRenderRobustness(t *testing.T) {
	deepNest := func(n int) string {
		var b strings.Builder
		b.WriteString("graph TD\n")
		for i := range n {
			b.WriteString(strings.Repeat("  ", i+1) + "subgraph s" + itoaR(i) + "\n")
		}
		b.WriteString(strings.Repeat("  ", n+1) + "A --> B\n")
		for range n {
			b.WriteString("end\n")
		}
		return b.String()
	}
	hugeEdges := func(n int) string {
		var b strings.Builder
		b.WriteString("graph TD\n")
		for i := range n {
			b.WriteString("  a" + itoaR(i%50) + " --> b" + itoaR((i*7)%50) + "\n")
		}
		return b.String()
	}

	cases := map[string]string{
		"empty":              "",
		"whitespace":         "   \n\t\n  ",
		"control_chars":      "graph TD\n  A\x00\x01\x02 --> B\x1b[31m",
		"deep_nest_100":      deepNest(100),
		"deep_nest_500":      deepNest(500),
		"huge_edges_10000":   hugeEdges(10000),
		"unterminated_fence": "graph TD\n  subgraph x\n    A --> B",
		"only_arrows":        "graph TD\n  --> --> -->\n  |||\n  <-->",
		"self_ref_cycle":     "graph TD\n  A --> A\n  A --> B\n  B --> A\n  B --> B",
		"long_label":         "graph TD\n  A[" + strings.Repeat("x", 5000) + "] --> B",
		"many_participants":  "sequenceDiagram\n" + strings.Repeat("  participant P\n", 300),
		"unicode_soup":       "graph TD\n  A[\u2764\ufe0f\U0001F468\u200d\U0001F469\u200d\U0001F467\U0001F1EF\U0001F1F5\u65e5\u672c\u8a9e\u0301] --> B",
		"class_huge_members": "classDiagram\n  class C {\n" + strings.Repeat("    +m() int\n", 200) + "  }",
		"nested_alt_deep":    "sequenceDiagram\n" + strings.Repeat("  alt x\n", 100) + "  A->>B: x\n" + strings.Repeat("  end\n", 100),
		"binary_garbage":     "graph TD\n\xff\xfe\xfd\x00 A --> \xc0\xc1 B",
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			var panicked any
			go func() {
				defer func() { panicked = recover(); close(done) }()
				_, _ = Render(src)
			}()
			select {
			case <-done:
				if panicked != nil {
					t.Fatalf("Render panicked on %q: %v", name, panicked)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("Render did not terminate (>10s) on %q", name)
			}
		})
	}
}

// FuzzRender asserts Render never panics on arbitrary input.
func FuzzRender(f *testing.F) {
	for _, s := range []string{
		"", "graph TD\n A --> B", "sequenceDiagram\n A->>B: x",
		"classDiagram\n class C", "erDiagram\n A ||--o{ B : x",
		"stateDiagram-v2\n [*] --> A", "subgraph\nend", "graph\n-->", "```",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Render panicked on %q: %v", src, r)
			}
		}()
		_, _ = Render(src)
	})
}

func itoaR(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
