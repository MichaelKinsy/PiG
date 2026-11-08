package mermaid

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Fuzz targets for the lovely-mermaid 0.3.3 parsers. Each target is seeded from
// the golden corpora (testdata/*corpus.txt) filtered to the grammar under test,
// asserts that nothing panics, that the parsed model respects the size caps and
// that Render's output stays bounded (maxCanvasCells) and terminates quickly.
// Run one with: go test -run '^$' -fuzz FuzzParsePie -fuzztime 10m ./internal/mermaid

const (
	// fuzzMaxSource keeps one execution cheap: the caps bound the output, not the
	// parse cost of an arbitrarily long input (see TestRenderRobustness).
	fuzzMaxSource = 16 << 10
	// fuzzMaxRender fails an execution that stops making progress.
	fuzzMaxRender = 5 * time.Second
)

// goldenSources returns the sources of the golden corpora that contain every
// marker in want (or all of them when want is empty).
func goldenSources(tb testing.TB, want ...string) []string {
	tb.Helper()
	var out []string
	for _, file := range []string{"testdata/corpus.txt", "testdata/parse-corpus.txt", "testdata/labels-corpus.txt"} {
		data, err := os.ReadFile(file)
		if err != nil {
			tb.Fatal(err)
		}
		for raw := range strings.SplitSeq(string(data), "\n") {
			if raw == "" || strings.HasPrefix(raw, "#") {
				continue
			}
			var src string
			if err := json.Unmarshal([]byte(raw), &src); err != nil {
				tb.Fatalf("%s: corpus line %q: %v", file, raw, err)
			}
			ok := true
			for _, w := range want {
				if !strings.Contains(src, w) {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, src)
			}
		}
	}
	return out
}

func seedFuzz(f *testing.F, extra []string, want ...string) {
	f.Helper()
	for _, s := range goldenSources(f, want...) {
		f.Add(s)
	}
	for _, s := range extra {
		f.Add(s)
	}
}

// checkRender renders src and asserts the output is bounded and prompt.
func checkRender(t *testing.T, src string) {
	t.Helper()
	start := time.Now()
	art, ok := Render(src)
	if d := time.Since(start); d > fuzzMaxRender {
		t.Fatalf("Render took %v on %q", d, src)
	}
	if !ok {
		return
	}
	if len(art.Styled) != len(art.Plain) {
		t.Fatalf("styled rows %d != plain rows %d on %q", len(art.Styled), len(art.Plain), src)
	}
	if cells := len(art.Plain) * max(art.Width, 1); cells > 2*maxCanvasCells {
		t.Fatalf("art is %d rows x %d cols (%d cells, cap %d) on %q", len(art.Plain), art.Width, cells, maxCanvasCells, src)
	}
}

// skipLong drops inputs too long for a cheap execution.
func skipLong(t *testing.T, src string) {
	t.Helper()
	if len(src) > fuzzMaxSource {
		t.Skip()
	}
}

func FuzzParsePie(f *testing.F) {
	seedFuzz(f, []string{
		"pie showData title Pets\n  \"Dogs\" : 386\n  \"Cats\" : 85.5",
		"pie\n  \"a\" : 1e309\n  \"b\" : -3\n  \"c\" : 0",
	}, "pie")
	f.Fuzz(func(t *testing.T, src string) {
		skipLong(t, src)
		if m := parsePie(src); m != nil && len(m.slices) > maxNodes {
			t.Fatalf("pie has %d slices, cap %d, on %q", len(m.slices), maxNodes, src)
		}
		checkRender(t, "pie\n"+src)
		checkRender(t, src)
	})
}

func FuzzParseMindmap(f *testing.F) {
	seedFuzz(f, []string{
		"mindmap\n  root((Root))\n    A[square]\n      B(round)\n    C))bang((\n      D)cloud(\n::icon(fa fa-book)",
		"mindmap\n" + strings.Repeat("  ", 40) + "deep\n\tTab\n\t\tTab2",
	}, "mindmap")
	f.Fuzz(func(t *testing.T, src string) {
		skipLong(t, src)
		roots, _, ok := parseMindmap(src)
		if ok {
			var count func(ns []*mindNode) int
			count = func(ns []*mindNode) int {
				n := 0
				for _, m := range ns {
					n += 1 + count(m.children)
				}
				return n
			}
			if n := count(roots); n > maxNodes {
				t.Fatalf("mindmap has %d nodes, cap %d, on %q", n, maxNodes, src)
			}
		}
		checkRender(t, "mindmap\n"+src)
		checkRender(t, src)
	})
}

func FuzzParseTimeline(f *testing.F) {
	seedFuzz(f, []string{
		"timeline\n  title History\n  section A\n    2002 : LinkedIn : Facebook\n    2004 : Google",
		"timeline\n  : orphan\n  1 : a : b : c\n  section\n  section x : y",
	}, "timeline")
	f.Fuzz(func(t *testing.T, src string) {
		skipLong(t, src)
		_, rows, _, ok := parseTimeline(src)
		if ok && len(rows) > maxEdges {
			t.Fatalf("timeline has %d rows, cap %d, on %q", len(rows), maxEdges, src)
		}
		checkRender(t, "timeline\n"+src)
		checkRender(t, src)
	})
}

func FuzzParseGitGraph(f *testing.F) {
	seedFuzz(f, []string{
		"gitGraph\n  commit id: \"a\" tag: \"v1\" type: HIGHLIGHT\n  branch dev\n  checkout dev\n  commit\n  checkout main\n  merge dev\n  cherry-pick id: \"a\"",
		"gitGraph\n  merge nope\n  checkout nope\n  branch main\n  branch x order: 3\n  commit id:\n  cherry-pick",
	}, "gitGraph")
	f.Fuzz(func(t *testing.T, src string) {
		skipLong(t, src)
		_, commits, _, _, ok := parseGitGraph(src)
		if ok && len(commits) > maxEdges {
			t.Fatalf("gitGraph has %d commits, cap %d, on %q", len(commits), maxEdges, src)
		}
		checkRender(t, "gitGraph\n"+src)
		checkRender(t, src)
	})
}

func FuzzFrontmatter(f *testing.F) {
	seedFuzz(f, []string{
		"---\ntitle: Hello\n---\ngraph TD\n  A --> B",
		"---\ntitle: \"Quoted: title\"\nconfig:\n  title: nested\n---\npie\n  \"a\" : 1",
		"---\ntitle: '\n---\n---\n---\ngraph TD\n A",
		"\n\n---\n---\ngraph LR\n  A",
		"---\ntitle: never closed\ngraph TD\n  A --> B",
	}, "---")
	f.Fuzz(func(t *testing.T, src string) {
		skipLong(t, src)
		lines := srcLines(src)
		if end := frontmatterEnd(lines); end < 0 || end > len(lines)+1 {
			t.Fatalf("frontmatterEnd %d out of range for %d lines on %q", end, len(lines), src)
		}
		if title, ok := frontmatterTitle(src); ok && title == "" {
			t.Fatalf("frontmatterTitle ok with empty title on %q", src)
		}
		_ = statementsOf(src)
		checkRender(t, src)
		checkRender(t, "---\n"+src+"\n---\ngraph TD\n  A --> B")
	})
}

func FuzzAtShape(f *testing.F) {
	seedFuzz(f, []string{
		"graph TD\n  A@{ shape: cyl, label: \"DB, with comma }\" } --> B@{shape: diam}",
		"graph TD\n  A@{ shape: nope, label: x\n  B@{ \"unclosed: {a: {b}}",
		"graph LR\n  A@{shape:hex,label:\"\"}-->B@{}",
	}, "@{")
	f.Fuzz(func(t *testing.T, body string) {
		skipLong(t, body)
		chars := []rune(body)
		sh := readAtShape(chars, 0)
		if sh.after < 0 || sh.after > len(chars)+1 {
			t.Fatalf("readAtShape after=%d out of range for %d chars on %q", sh.after, len(chars), body)
		}
		g := parseGraph("graph TD\n  A@{" + body + "}\n  A --> B@{" + body)
		if g != nil && (len(g.nodes) > maxNodes || len(g.edges) > maxEdges) {
			t.Fatalf("graph has %d nodes / %d edges beyond caps on %q", len(g.nodes), len(g.edges), body)
		}
		checkRender(t, "graph TD\n  A@{"+body+"}\n  A --> B")
	})
}

func FuzzClassDef(f *testing.F) {
	seedFuzz(f, []string{
		"graph TD\n  A:::hot --> B\n  classDef hot fill:#f96,stroke:#333,color:rgb(255,0,0),font-weight:bold\n  class B hot,cold\n  click A href \"https://example.com\"",
		"graph TD\n  classDef __proto__ fill:red\n  classDef a,b fill:#fff,fill\n  class A,B a",
		"stateDiagram-v2\n  classDef s fill:hsl(10 20% 30%)\n  [*] --> A:::s",
	}, "classDef")
	f.Fuzz(func(t *testing.T, src string) {
		skipLong(t, src)
		if names, props, ok := parseClassDef(src); ok {
			if len(names) == 0 {
				t.Fatalf("parseClassDef ok with no names on %q", src)
			}
			classDefs := map[string]map[string]string{}
			for _, n := range names {
				classDefs[n] = props
			}
			_ = ResolveClassStyle(names, classDefs)
		}
		_, _ = parseClassAssign(src)
		_, _ = parseHref(src)
		checkRender(t, src)
		checkRender(t, "graph TD\n  A:::x --> B\n  classDef x "+src+"\n  class B x")
	})
}
