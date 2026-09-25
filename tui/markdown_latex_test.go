package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// markdownLatexGolden is one line of testdata/markdown-latex-golden.jsonl,
// captured from pi's own Markdown component by parity/testdata/markdown-latex-pi.mjs.
type markdownLatexGolden struct {
	Input string   `json:"input"`
	Lines []string `json:"lines"`
}

// TestMarkdownLatexMatchesUpstream diffs pig's Markdown render (ANSI-stripped,
// like markdownPlainLines) against pi's over a markdown+math corpus at width 80,
// proving the inline/block latex tokenizer integration is faithful.
func TestMarkdownLatexMatchesUpstream(t *testing.T) {
	f, err := os.Open("testdata/markdown-latex-golden.jsonl")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()

	var total, mismatches int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var g markdownLatexGolden
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("bad golden %q: %v", sc.Bytes(), err)
		}
		total++
		got := markdownPlainLines(NewMarkdown(g.Input).Render(80))
		if !equalLines(got, g.Lines) {
			mismatches++
			t.Errorf("input %q:\n  got:  %#v\n  want: %#v", g.Input, got, g.Lines)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total == 0 {
		t.Fatal("no goldens loaded")
	}
	t.Logf("checked %d inputs; %d mismatches", total, mismatches)
}

func equalLines(a, b []string) bool {
	// pi trims trailing whitespace per line; markdownPlainLines already does too.
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimRight(a[i], " ") != strings.TrimRight(b[i], " ") {
			return false
		}
	}
	return true
}
