package codingagent

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestMermaidTransformMatchesUpstream diffs pig's Mermaid markdown transformer
// against pi's own createMermaidMarkdownTransformer (compiled mermaid.js, no
// theme → plain art) over render / gating / warnings / width / identity cases.
func TestMermaidTransformMatchesUpstream(t *testing.T) {
	f, err := os.Open("testdata/mermaid-transform-golden.jsonl")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()

	var total, changed int
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
	t.Logf("checked %d byte-exact transform cases (%d rewrote the markdown)", total, changed)
}
