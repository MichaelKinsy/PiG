package mermaid

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestRenderMatchesUpstream diffs pig's full render() pipeline against pi's
// bundled grok-mermaid@0.2.2 render() over every diagram type, fallback (null),
// and streaming case: the end-to-end gate for parse + layout + canvas + index.

func styledJSON(styled [][]Span) []any {
	rows := []any{}
	for _, row := range styled {
		cells := []any{}
		for _, sp := range row {
			cells = append(cells, map[string]any{"text": sp.Text, "cls": string(sp.Cls)})
		}
		rows = append(rows, cells)
	}
	return rows
}

func artJSON(a Art) map[string]any {
	return map[string]any{
		"plain":    strsJSON(a.Plain),
		"styled":   styledJSON(a.Styled),
		"width":    a.Width,
		"warnings": strsJSON(a.Warnings),
	}
}

func TestRenderMatchesUpstream(t *testing.T) {
	f, err := os.Open("testdata/render-golden.jsonl")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()

	var total, nullArt int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var g struct {
			Input string          `json:"input"`
			Kind  any             `json:"kind"`
			Art   json.RawMessage `json:"art"`
		}
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("bad golden %q: %v", sc.Bytes(), err)
		}
		total++

		var piArt any
		if err := json.Unmarshal(g.Art, &piArt); err != nil {
			t.Fatalf("bad golden art %q: %v", g.Art, err)
		}

		art, ok := Render(g.Input)
		if piArt == nil {
			nullArt++
			if ok {
				aj, _ := json.Marshal(artJSON(art))
				t.Errorf("render(%q): got art, want null\n  go: %s", g.Input, aj)
			}
			continue
		}
		if !ok {
			t.Errorf("render(%q): got null, want art", g.Input)
			continue
		}
		goArt := canonical(t, artJSON(art))
		if !reflect.DeepEqual(goArt, piArt) {
			gj, _ := json.Marshal(goArt)
			pj, _ := json.Marshal(piArt)
			t.Errorf("render(%q) mismatch\n  go: %s\n  pi: %s", g.Input, gj, pj)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total == 0 {
		t.Fatal("no goldens")
	}
	t.Logf("checked %d sources (%d null-art) end-to-end", total, nullArt)
}
