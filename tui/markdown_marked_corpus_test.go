package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestMarkdownMarkedCorpusMatchesPi renders every case of testdata/markdown-marked-corpus.json and compares the exact rendered bytes with pinned pi-tui's Markdown output in testdata/markdown-marked-golden.jsonl. The corpus holds the CommonMark 0.31.2 examples of "Emphasis and strong emphasis", "HTML blocks", "Raw HTML", "Code spans", "Hard line breaks", "Autolinks", "Backslash escapes", "Paragraphs", "Tabs" and "Link reference definitions", the reference links of "Links" (526-571), the "Images" examples with a definition and the "List items" examples with a tab, which marked 18.0.11 tokenizes, plus underscore, delimiter-run, adversarial-run, inline/block HTML, lexer-state, paragraph-to-table, definition-placement, list-item code, latex-block paragraph-clipping and table escaped-pipe (helpers.ts splitCells backslash parity) cases. test/parity/testdata/markdown-marked-pi.mjs produces the golden from Pi's own component with the packages/tui/test/test-themes.ts theme.
func TestMarkdownMarkedCorpusMatchesPi(t *testing.T) {
	mdCaseSetup(t)
	data, err := os.ReadFile("testdata/markdown-marked-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Width  int    `json:"width"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	golden := map[string][]string{}
	records, err := os.ReadFile("testdata/markdown-marked-golden.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for line := range bytes.Lines(records) {
		var record struct {
			Name  string   `json:"name"`
			Lines []string `json:"lines"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		golden[record.Name] = record.Lines
	}
	if len(corpus) == 0 || len(golden) != len(corpus) {
		t.Fatalf("corpus has %d cases, golden has %d records", len(corpus), len(golden))
	}
	for _, c := range corpus {
		want, ok := golden[c.Name]
		if !ok {
			t.Fatalf("no golden for %s", c.Name)
		}
		t.Run(c.Name, func(t *testing.T) {
			got := NewMarkdownWithOptions(c.Source, 0, 0, mdUpstreamTheme(), nil, nil).Render(c.Width)
			if !slices.Equal(got, want) {
				t.Errorf("source %q\n got %q\nwant %q", c.Source, got, want)
			}
		})
	}
}
