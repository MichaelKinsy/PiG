package codingagent

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// isMermaidHintLine reports whether s is exactly one appended D50 hint line:
// a leading newline, the hint as an inline code span, and the two-space hard
// break. Anything else is real drift.
func isMermaidHintLine(s string) bool {
	return strings.HasPrefix(s, "\n`Mermaid diagram not rendered: ") && strings.HasSuffix(s, "`  \n") &&
		!strings.Contains(strings.TrimSuffix(strings.TrimPrefix(s, "\n"), "  \n"), "\n")
}

// TestMermaidTransformMatchesUpstream diffs pig's Mermaid markdown transformer
// against pi's own createMermaidMarkdownTransformer (compiled mermaid.js, no
// theme → plain art) over render / gating / warnings / width / identity cases.
func TestMermaidTransformMatchesUpstream(t *testing.T) {
	f, err := os.Open("testdata/mermaid-transform-golden.jsonl")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()

	var total, changed, hinted, fitted int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var g struct {
			Input struct {
				MD             string `json:"md"`
				Mode           string `json:"mode"`
				MessageType    string `json:"messageType"`
				IsStreaming    bool   `json:"isStreaming"`
				AvailableWidth int    `json:"availableWidth"`
			} `json:"input"`
			Out string `json:"out"`
		}
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("bad golden %q: %v", sc.Bytes(), err)
		}
		total++
		if g.Out != g.Input.MD {
			changed++
		}

		transformer := createMermaidMarkdownTransformer(func() string { return g.Input.Mode }, nil)
		got := transformer(g.Input.MD, extension.MarkdownTransformContext{
			MessageType:    extension.MarkdownMessageType(g.Input.MessageType),
			IsStreaming:    g.Input.IsStreaming,
			AvailableWidth: g.Input.AvailableWidth,
		})
		if got != g.Out {
			// pig divergence (D50): where pi silently returns the raw block for a
			// diagram it cannot draw, pig appends one "Mermaid diagram not
			// rendered: …" hint. The allowance is exact: pig's output must be
			// pi's output plus a single well-formed hint line: so a dropped
			// diagram, altered art, or any other byte drift still fails.
			if extra, cut := strings.CutPrefix(got, g.Out); cut && isMermaidHintLine(extra) {
				hinted++
				continue
			}
			// pig divergence (D58): where pi returns the raw block because the
			// diagram is wider than the area, pig narrows the node labels until
			// the art fits and draws it. Accepted only when pi showed the raw
			// source and pig's art really is inside the area; pig drawing
			// something pi also drew must still match byte for byte.
			if g.Out == g.Input.MD && fitsAsDrawnDiagram(got, g.Input.AvailableWidth) {
				fitted++
				continue
			}
			t.Errorf("transform mismatch (mode=%s msg=%s stream=%v w=%d)\n  md:  %q\n  go:  %q\n  pi:  %q",
				g.Input.Mode, g.Input.MessageType, g.Input.IsStreaming, g.Input.AvailableWidth, g.Input.MD, got, g.Out)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total == 0 {
		t.Fatal("no goldens")
	}
	t.Logf("checked %d transform cases (%d rewrote the markdown, %d differ only by the D50 hint, %d drawn by D58 narrowing where pi showed source)", total, changed, hinted, fitted)
}

// fitsAsDrawnDiagram reports whether out is drawn Mermaid art whose every row
// fits availableWidth. Each row is one Markdown code span, so a row that is not
// one means the transform emitted something other than a diagram and the case is
// not a D58 narrowing.
func fitsAsDrawnDiagram(out string, availableWidth int) bool {
	rows := strings.Split(strings.TrimSuffix(out, "\n"), "  \n")
	if len(rows) == 0 || availableWidth <= 0 {
		return false
	}
	for _, row := range rows {
		if !strings.HasPrefix(row, "`") || !strings.HasSuffix(row, "`") {
			return false
		}
		if widthx.VisibleWidth(strings.Trim(row, "`")) > availableWidth {
			return false
		}
	}
	return true
}
