package mermaid

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

type labelsGolden struct {
	Input    string   `json:"input"`
	Clean    string   `json:"clean"`
	HTMLTags string   `json:"htmlTags"`
	Markdown string   `json:"markdown"`
	Entities string   `json:"entities"`
	Lower    string   `json:"lower"`
	Upper    string   `json:"upper"`
	Wrap     []string `json:"wrap"`
	Fit      string   `json:"fit"`
	SrcLines []string `json:"srcLines"`
}

// TestLabelsMatchUpstream diffs pig's label helpers against grok-mermaid's own
// exported functions (labels.js) over an entity/tag/markdown/wrapping corpus.
func TestLabelsMatchUpstream(t *testing.T) {
	f, err := os.Open("testdata/labels-golden.jsonl")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()

	var total int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var g labelsGolden
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("bad golden %q: %v", sc.Bytes(), err)
		}
		total++
		eq := func(name, got, want string) {
			if got != want {
				t.Errorf("%s(%q) = %q, want %q", name, g.Input, got, want)
			}
		}
		eq("cleanLabel", cleanLabel(g.Input), g.Clean)
		eq("stripHtmlTags", stripHtmlTags(g.Input), g.HTMLTags)
		eq("stripMarkdown", stripMarkdown(g.Input), g.Markdown)
		eq("decodeHtmlEntities", decodeHtmlEntities(g.Input), g.Entities)
		eq("asciiLower", asciiLower(g.Input), g.Lower)
		eq("asciiUpper", asciiUpper(g.Input), g.Upper)
		eq("fitLabel", fitLabel(g.Input, 28), g.Fit)
		if got, _ := wrapLabel(g.Input, 24, 4); !equalStrs(got, g.Wrap) {
			t.Errorf("wrapLabel(%q,24,4) = %#v, want %#v", g.Input, got, g.Wrap)
		}
		if got := srcLines(g.Input); !equalStrs(got, g.SrcLines) {
			t.Errorf("srcLines(%q) = %#v, want %#v", g.Input, got, g.SrcLines)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total == 0 {
		t.Fatal("no goldens")
	}
	t.Logf("checked %d label inputs across 9 helpers", total)
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
