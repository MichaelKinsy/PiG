package mermaid

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestParseMatchesUpstream diffs pig's parser model against lovely-mermaid@0.3.3's
// own exported parse functions (diagrams/*.js), structurally, before layout lands.
// It covers diagramKind dispatch and the parsed nodes/edges/groups/sections/
// classes/hrefs/classDefs/items/activations of the five diagram types that export
// a parser, plus every fallback (null-model) path.

func strsJSON(s []string) []any {
	out := []any{}
	for _, x := range s {
		out = append(out, x)
	}
	return out
}

func pstr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nodesJSON(nodes []node) []any {
	out := []any{}
	for _, n := range nodes {
		m := map[string]any{"label": n.label, "shape": string(n.shape)}
		if n.sections != nil {
			sections := []any{}
			for _, sec := range n.sections {
				sections = append(sections, strsJSON(sec))
			}
			m["sections"] = sections
		}
		if n.classes != nil {
			m["classes"] = strsJSON(n.classes)
		}
		if n.href != "" {
			m["href"] = n.href
		}
		out = append(out, m)
	}
	return out
}

func edgesJSON(edges []edge) []any {
	out := []any{}
	for _, e := range edges {
		m := map[string]any{
			"from": e.from, "to": e.to, "label": pstr(e.label),
			"headTo": string(e.headTo), "headFrom": string(e.headFrom), "line": string(e.line),
		}
		if e.cardFrom != "" {
			m["cardFrom"] = e.cardFrom
		}
		if e.cardTo != "" {
			m["cardTo"] = e.cardTo
		}
		out = append(out, m)
	}
	return out
}

func groupsJSON(groups []group) []any {
	out := []any{}
	for _, g := range groups {
		var parent any
		if g.parent != nil {
			parent = *g.parent
		}
		out = append(out, map[string]any{"id": g.id, "label": g.label, "parent": parent})
	}
	return out
}

func nodeGroupJSON(ng []*int) []any {
	out := []any{}
	for _, p := range ng {
		if p == nil {
			out = append(out, nil)
		} else {
			out = append(out, *p)
		}
	}
	return out
}

func truncatedJSON(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func graphModelJSON(g *graph) map[string]any {
	return map[string]any{
		"nodes": nodesJSON(g.nodes), "edges": edgesJSON(g.edges),
		"dir": string(g.dir), "groups": groupsJSON(g.groups), "nodeGroup": nodeGroupJSON(g.nodeGroup),
		"warnings": strsJSON(g.warnings), "classDefs": classDefsJSON(g.classDefs),
		"truncated": truncatedJSON(g.truncated),
	}
}

func anchorJSON(a noteAnchor) map[string]any {
	if a.kind == noteOver {
		return map[string]any{"kind": string(a.kind), "from": a.from, "to": a.to}
	}
	return map[string]any{"kind": string(a.kind), "at": a.at}
}

func seqModelJSON(s *sequence) map[string]any {
	items := []any{}
	for _, it := range s.items {
		switch it.kind {
		case seqMessage:
			items = append(items, map[string]any{
				"kind": "message", "from": it.from, "to": it.to,
				"text": pstr(it.text), "dashed": it.dashed, "head": string(it.head),
			})
		case seqNote:
			items = append(items, map[string]any{"kind": "note", "anchor": anchorJSON(it.anchor), "text": it.str})
		case seqDivider:
			items = append(items, map[string]any{"kind": "divider", "text": it.str})
		}
	}
	activations := []any{}
	for _, a := range s.activations {
		var to any
		if a.to != activationOpen {
			to = a.to
		}
		activations = append(activations, map[string]any{"at": a.at, "from": a.from, "to": to})
	}
	return map[string]any{
		"labels": strsJSON(s.labels), "items": items, "activations": activations,
		"warnings": strsJSON(s.warnings), "truncated": truncatedJSON(s.truncated),
	}
}

// buildParseModel mirrors mermaid-parse-pi.mjs: render() strips control
// characters before dispatching, so the parsers see the stripped source.
func buildParseModel(src string) any {
	src = stripControls(src)
	switch diagramKind(src) {
	case "flowchart":
		if g := parseGraph(src); g != nil {
			return graphModelJSON(g)
		}
	case "state":
		if g := parseState(src); g != nil {
			return graphModelJSON(g)
		}
	case "class":
		if g := parseClass(src); g != nil {
			return graphModelJSON(g)
		}
	case "er":
		if g := parseEr(src); g != nil {
			return graphModelJSON(g)
		}
	case "sequence":
		if s := parseSequence(src); s != nil {
			return seqModelJSON(s)
		}
	}
	return nil
}

func canonical(t *testing.T, v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestParseMatchesUpstream(t *testing.T) {
	f, err := os.Open("testdata/parse-golden.jsonl.gz")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()
	unzipped, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gunzip goldens: %v", err)
	}

	var total, nullModels int
	sc := bufio.NewScanner(unzipped)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var g struct {
			Input string          `json:"input"`
			Kind  string          `json:"kind"`
			Model json.RawMessage `json:"model"`
		}
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("bad golden %q: %v", sc.Bytes(), err)
		}
		total++

		if got := diagramKind(g.Input); got != g.Kind {
			t.Errorf("diagramKind(%q) = %q, want %q", g.Input, got, g.Kind)
		}

		var piModel any
		if err := json.Unmarshal(g.Model, &piModel); err != nil {
			t.Fatalf("bad golden model %q: %v", g.Model, err)
		}
		if piModel == nil {
			nullModels++
		}
		goModel := canonical(t, buildParseModel(g.Input))
		if !reflect.DeepEqual(goModel, piModel) {
			gj, _ := json.Marshal(goModel)
			pj, _ := json.Marshal(piModel)
			t.Errorf("parse(%q) model mismatch\n  go: %s\n  pi: %s", g.Input, gj, pj)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total == 0 {
		t.Fatal("no goldens")
	}
	t.Logf("checked %d sources (%d null-model fallbacks) across the 5 diagram types with an exported parser", total, nullModels)
}
