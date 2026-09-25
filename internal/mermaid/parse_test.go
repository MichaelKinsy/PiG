package mermaid

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestParseMatchesUpstream diffs pig's parser model against grok-mermaid's own
// exported parse functions (parse.js), structurally, before layout lands. It
// covers diagramKind dispatch and the parsed nodes/edges/groups/infos/items of
// all five diagram types plus every fallback (null-model) path.

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
		out = append(out, map[string]any{"label": n.label, "shape": string(n.shape)})
	}
	return out
}

func edgesJSON(edges []edge) []any {
	out := []any{}
	for _, e := range edges {
		out = append(out, map[string]any{
			"from": e.from, "to": e.to, "label": pstr(e.label),
			"headTo": string(e.headTo), "headFrom": string(e.headFrom), "line": string(e.line),
		})
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

func infosJSON(infos []classInfo) []any {
	out := []any{}
	for _, ci := range infos {
		out = append(out, map[string]any{
			"annotation": pstr(ci.annotation), "attrs": strsJSON(ci.attrs), "methods": strsJSON(ci.methods),
		})
	}
	return out
}

func graphModelJSON(g *graph) map[string]any {
	return map[string]any{
		"nodes": nodesJSON(g.nodes), "edges": edgesJSON(g.edges),
		"dir": string(g.dir), "groups": groupsJSON(g.groups), "warnings": strsJSON(g.warnings),
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
	return map[string]any{"labels": strsJSON(s.labels), "items": items}
}

func buildParseModel(src string) any {
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
		if g, infos, ok := parseClass(src); ok {
			m := graphModelJSON(g)
			m["infos"] = infosJSON(infos)
			return m
		}
	case "er":
		if g, infos, ok := parseEr(src); ok {
			m := graphModelJSON(g)
			m["infos"] = infosJSON(infos)
			return m
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
	f, err := os.Open("testdata/parse-golden.jsonl")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()

	var total, nullModels int
	sc := bufio.NewScanner(f)
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
	t.Logf("checked %d sources (%d null-model fallbacks) across 5 diagram types", total, nullModels)
}
