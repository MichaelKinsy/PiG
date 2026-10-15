// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"html"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/dlclark/regexp2"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const sampleGo = `package sample

// Widget is shown.
type Widget struct {
	Name string
	// Size is bounded.
	Size int
}

func (w *Widget) Grow() {
	// pig divergence (D7): growth is capped.
	w.Size++
}

func Plain() {}

const Limit = 3
`

func TestSymIndexResolvesTypesMembersMethodsAndValues(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sample/w.go", sampleGo)
	x := newSymIndex(dir)
	for _, c := range []struct {
		sym        string
		start, end int
	}{
		{"Widget", 4, 8},
		{"Widget.Size", 7, 7},
		{"Widget.Grow", 10, 13},
		{"Plain", 15, 15},
		{"Limit", 17, 17},
	} {
		r := x.resolve("sample/w.go", c.sym)
		if r.Line != c.start || r.End != c.end {
			t.Errorf("%s: lines %d-%d, want %d-%d", c.sym, r.Line, r.End, c.start, c.end)
		}
	}
	if r := x.resolve("sample/w.go", "Nope"); r.Line != 0 || r.Missing {
		t.Errorf("an unknown symbol keeps its file and has no line: %+v", r)
	}
	if r := x.resolve("sample/gone.go", "Widget"); !r.Missing {
		t.Errorf("a missing file is reported missing: %+v", r)
	}
	if sp, ok := x.enclosing("sample/w.go", 11); !ok || sp.Start != 10 {
		t.Errorf("the line inside Grow is enclosed by Grow: %+v", sp)
	}
}

func TestGoSnippetCentersTheMarkerAndHighlights(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sample/w.go", sampleGo)
	x := newSymIndex(dir)
	sn := goSnippet(x, Site{"sample/w.go", 11})
	if sn.Start != 10 || sn.End != 13 || sn.Unavailable != "" {
		t.Fatalf("the snippet is the enclosing method: %+v", sn)
	}
	if !strings.Contains(sn.HTML, `<span class="kd">func</span>`) || !strings.Contains(sn.HTML, `<span class="c1">// pig divergence (D7)`) {
		t.Errorf("the code is not highlighted: %s", sn.HTML)
	}
	if got := goSnippet(x, Site{"sample/none.go", 3}); got.Unavailable == "" {
		t.Errorf("a missing file says so: %+v", got)
	}
}

func TestPiSnippetResolvesCitationsAndReportsGaps(t *testing.T) {
	mirror := t.TempDir()
	writeFile(t, mirror, "packages/ai/src/api/thing.ts", "export function a() {\n  return 1;\n}\nexport const b = `x`;\n")
	writeFile(t, mirror, "packages/tui/src/thing.ts", "// short\n")
	pi := newPiSource(mirror)
	sn := piSnippet(pi, Cite{"api/thing.ts", 1, 3}, "9.9.9")
	if sn.Path != "packages/ai/src/api/thing.ts" || sn.Start != 1 || sn.End != 3 || !strings.Contains(sn.HTML, `<span class="kd">function</span>`) && !strings.Contains(sn.HTML, `<span class="k`) {
		t.Errorf("suffix match, lines and highlighting: %+v", sn)
	}
	if got := plainText(sn.HTML); got != "export function a() {\n  return 1;\n}" {
		t.Errorf("the snippet shows the cited lines 1-3: %q", got)
	}
	if got := plainText(piSnippet(pi, Cite{"api/thing.ts", 4, 4}, "9.9.9").HTML); got != "export const b = `x`;" {
		t.Errorf("a one-line citation shows that line: %q", got)
	}
	// Two files share a base name; the one long enough to hold the range wins.
	if sn := piSnippet(pi, Cite{"thing.ts", 2, 3}, "9.9.9"); sn.Path != "packages/ai/src/api/thing.ts" {
		t.Errorf("the file that holds the cited lines wins: %+v", sn)
	}
	if sn := piSnippet(pi, Cite{"absent.ts", 1, 2}, "9.9.9"); !strings.Contains(sn.Unavailable, "not found in Pi 9.9.9") {
		t.Errorf("an unknown file is reported: %+v", sn)
	}
	if sn := piSnippet(pi, Cite{"thing.ts", 90, 99}, "9.9.9"); sn.Unavailable == "" {
		t.Errorf("a range past the end is reported: %+v", sn)
	}
	if sn := piSnippet(newPiSource(""), Cite{"thing.ts", 1, 2}, "9.9.9"); !strings.Contains(sn.Unavailable, "mirror is not present") {
		t.Errorf("an absent mirror is reported: %+v", sn)
	}
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// plainText is the source text of highlighted snippet markup.
func plainText(h string) string { return html.UnescapeString(tagRe.ReplaceAllString(h, "")) }

// Highlighting keeps the text: the lexer's closing newline, often inside the last token's span, does not become an extra snippet line.
func TestHighlightKeepsTheTextWithoutAnExtraLine(t *testing.T) {
	for _, c := range []struct{ lang, code string }{
		{"go", "x := 1 // the last token is a comment"},
		{"typescript", "const a = 1;\n// two lines, a comment last"},
		{"typescript", "return msg;\n});"},
		{"", "plain <text> & more"},
	} {
		if got := plainText(highlight(c.lang, c.code)); got != c.code {
			t.Errorf("%s: %q, want %q", c.lang, got, c.code)
		}
	}
}

// chroma gives every compiled rule a 250 ms match deadline and reads a missed one as no match, so on a loaded host the report
// tokenised the same snippet differently (TestOutputIsDeterministicAndSelfContained failed in the fan-in). highlight's lexers,
// and the template lexer the Go lexer delegates raw strings to, match without one.
func TestHighlightLexersHaveNoMatchDeadline(t *testing.T) {
	languages := []string{"go", "typescript", "javascript", "rust", "python", "toml", "json"}
	checked := map[chroma.Lexer]string{lexers.GoTextTemplate: "go text template"}
	for _, lang := range languages {
		highlight(lang, "x")
		checked[lexers.Get(lang)] = lang
	}
	for lexer, name := range checked {
		rules := compiledRules(lexer)
		if len(rules) == 0 {
			t.Errorf("%s: no compiled rules", name)
		}
		for state, list := range rules {
			for i, rule := range list {
				if rule.Regexp.MatchTimeout != regexp2.DefaultMatchTimeout {
					t.Errorf("%s: rule %s.%d matches with a %v deadline", name, state, i, rule.Regexp.MatchTimeout)
				}
			}
		}
	}
}

func TestFindCites(t *testing.T) {
	text := "Pi's `google-generative-ai.ts:195-200` and render-utils.ts:71\u201396, also config.ts:528-573; not a cite: v1.2:3 or node_modules/x/y.ts:5, big a.ts:1-9000."
	got := findCites(text)
	want := []Cite{{"google-generative-ai.ts", 195, 200}, {"render-utils.ts", 71, 96}, {"config.ts", 528, 573}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %v, want %v", i, got[i], want[i])
		}
	}
}

func TestScanTreeFindsMarkersTestsAndScenarios(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sample/w.go", sampleGo)
	writeFile(t, dir, "sample/w_test.go", "package sample\n\n// pig additive (D8, D9): x\nfunc TestGrow(t *testing.T) {}\n")
	writeFile(t, dir, "test/parity/scenarios/settings/10-one.toml", "x = 1\n")
	writeFile(t, dir, "node_modules/p/z.go", "// pig divergence (D99)\n")
	ts, err := scanTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s := ts.Markers["D7"]; len(s) != 1 || s[0] != (Site{"sample/w.go", 11}) {
		t.Errorf("D7: %v", s)
	}
	if len(ts.Markers["D8"]) != 1 || len(ts.Markers["D9"]) != 1 {
		t.Errorf("one marker names two records: %v", ts.Markers)
	}
	if len(ts.Markers["D99"]) != 0 {
		t.Error("a vendored directory is not scanned")
	}
	if s := ts.Tests["TestGrow"]; len(s) != 1 || s[0].Line != 4 {
		t.Errorf("TestGrow: %v", s)
	}
	if ts.Scenarios["settings/10-one"] != "test/parity/scenarios/settings/10-one.toml" {
		t.Errorf("scenarios: %v", ts.Scenarios)
	}
}

func TestRenderMarkdownLinksDivergencesAndNeutralizesTheRest(t *testing.T) {
	known := map[string]bool{"D63": true, "D2": true}
	got := renderMarkdown("See D63 and `D2` in [the note](DIVERGENCES.md#d63---version) and [a file](docs/x.md) and [Pi](https://example.test/a).\n\n<script>alert(1)</script>\n\n## Heading\n", known)
	for _, want := range []string{
		`<a href="#D63" data-d="D63">D63</a>`,
		`<code>D2</code>`,
		`<a href="#D63" data-d="D63">the note</a>`,
		`<span class="deadlink" title="docs/x.md">a file</span>`,
		`<a href="https://example.test/a" rel="noopener noreferrer">Pi</a>`,
		`<h4>Heading</h4>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if strings.Contains(got, "<script>") {
		t.Errorf("raw HTML survives: %s", got)
	}
}

func TestClassifyUsesTheBugLedgerBeforeKeywords(t *testing.T) {
	bugs := &BugLedger{ByDivergence: map[string][]*BugEntry{"D5": {{ID: "BF-x", Class: "ahead-of-pi"}}}}
	cases := []struct {
		d    Divergence
		want string
	}{
		{Divergence{ID: "D5", Title: "Windows path handling"}, ClassPiBug},
		{Divergence{ID: "D6", Title: "Bounded caches", PiIssues: []Link{{URL: "u"}}}, ClassPiBug},
		{Divergence{ID: "D6", Title: "Bounded caches"}, ClassPerf},
		{Divergence{ID: "D6", Title: "Windows owner-only files"}, ClassPlatform},
		{Divergence{ID: "D6", Title: "Separate command identity"}, ClassOther},
		{Divergence{ID: "D6", Title: "Piglets", Status: DivAdditive}, ClassAdditive},
		{Divergence{ID: "D6", Title: "Overflow", Raw: "Pi overflows the stack in wordWrapLine"}, ClassOther},
	}
	for _, c := range cases {
		if got, why := classify(&c.d, bugs); got != c.want || why == "" {
			t.Errorf("%s %q: class %q (%q), want %q", c.d.ID, c.d.Title, got, why, c.want)
		}
	}
}

func TestProofClassFollowsTheRowKind(t *testing.T) {
	byID := map[string]*mappingRow{"pkg:a/.#Thing::property:go::call:0": {}}
	cases := []struct {
		id, kind, typ, alias, status, want string
	}{
		{"pkg:a/.#X", "interface", "", "", StatusDesignedOut, ClassDesignOut},
		{"pkg:a/.#ExtensionAPI::property:on", "property", "", "", StatusPorted, ClassWire},
		{"pkg:a/.#AgentEvent::property:type", "property", "", "", StatusPorted, ClassWire},
		{"pkg:a/.#ToolDefinition::property:name", "property", "", "", StatusPorted, ClassRegistration},
		{"pkg:a/.#ProviderConfig::property:name", "property", "", "", StatusPorted, ClassRegistration},
		{"pkg:a/.#Mode", "type-alias", `"a" | "b"`, "", StatusPorted, ClassUnion},
		{"pkg:a/.#Fn<T>", "type-alias", `Array<A | B>`, "", StatusPorted, ClassNone},
		{"pkg:a/.#run", "function", "", "", StatusPorted, ClassBehaviour},
		{"pkg:a/.#Thing::property:go", "property", "", "", StatusPorted, ClassBehaviour},
		{"pkg:a/.#Thing::property:name", "property", "", "", StatusPorted, ClassNone},
		{"pkg:a/.#Thing", "class", "", "", StatusPorted, ClassBehaviour},
		{"cli:pi/--x", "cli-flag", "", "", StatusPorted, ClassNone},
	}
	for _, c := range cases {
		r := &Row{ID: c.id, Status: c.status}
		iv := &invRow{ID: c.id, Kind: c.kind, Shape: invShape{Type: c.typ, AliasTarget: c.alias}}
		if got := proofClass(r, iv, byID); got != c.want {
			t.Errorf("%s (%s): class %s, want %s", c.id, c.kind, got, c.want)
		}
	}
}

func TestTokenizeSplitsIdentifiersAndKeepsTheWhole(t *testing.T) {
	got := map[string]struct{}{}
	tokenize("agent/tool_call_hook_funcs.go#AfterToolCallContext::property:args", got)
	for _, w := range []string{"agent", "tool_call_hook_funcs", "after", "tool", "call", "context", "aftertoolcallcontext", "property", "args", "go"} {
		if _, ok := got[w]; !ok && w != "tool_call_hook_funcs" {
			t.Errorf("token %q is missing from %v", w, got)
		}
	}
	for _, w := range []string{"tool", "hook", "funcs"} {
		if _, ok := got[w]; !ok {
			t.Errorf("snake-case word %q is missing", w)
		}
	}
}

// The search index finds every row by each word of its own id: the posting lists decode to the rows that contain the token.
func TestSearchIndexIsCompleteForRowNames(t *testing.T) {
	rep, _ := built(t)
	pl := rep.Ledgers[0]
	ld, _ := buildLedgerData(pl, rep)
	if len(ld.Tok) == 0 || len(ld.Tok) != len(ld.Post) || !sort.StringsAreSorted(ld.Tok) {
		t.Fatalf("index shape: %d tokens, %d postings, sorted=%v", len(ld.Tok), len(ld.Post), sort.StringsAreSorted(ld.Tok))
	}
	lookup := func(tok string) map[int]bool {
		i := sort.SearchStrings(ld.Tok, tok)
		out := map[int]bool{}
		if i == len(ld.Tok) || ld.Tok[i] != tok {
			return out
		}
		acc := 0
		for _, d := range ld.Post[i] {
			acc += d
			out[acc] = true
		}
		return out
	}
	step := max(1, len(pl.Rows)/400)
	for ri := 0; ri < len(pl.Rows); ri += step {
		words := map[string]struct{}{}
		tokenize(strings.TrimPrefix(pl.Rows[ri].ID, "pkg:"), words)
		for w := range words {
			if !lookup(w)[ri] {
				t.Fatalf("row %d (%s) is not found by %q", ri, pl.Rows[ri].ID, w)
			}
		}
	}
}

func TestRationaleTemplatesRoundTrip(t *testing.T) {
	r := &Row{Name: "Foo::property:bar", Targets: []Ref{{File: "a/b.go", Sym: "Foo.Bar"}}, Rationale: "Derived: Foo::property:bar is Go a/b.go#Foo.Bar (name rule N1)."}
	tpl := templateRationale(r)
	if strings.Contains(tpl, "Foo") || !strings.Contains(tpl, "\x03") || !strings.Contains(tpl, "\x01") {
		t.Fatalf("placeholders: %q", tpl)
	}
	// The client expands placeholders the same way.
	out := regexp.MustCompile("\x01(\\d+)\x02").ReplaceAllStringFunc(tpl, func(m string) string {
		n, _ := strconv.Atoi(strings.Trim(m, "\x01\x02"))
		return r.Targets[n].File + "#" + r.Targets[n].Sym
	})
	out = strings.ReplaceAll(out, "\x03", r.Name)
	if out != r.Rationale {
		t.Errorf("round trip: %q", out)
	}
}

// A row the gap baseline lists is a gap whatever its disposition says, a pending row is a gap, and the rest follow the disposition and the rationale. The ledger loads when the optional proof-class baseline is absent.
func TestLoadPinLedgerStatusFollowsDispositionAndGapBaseline(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "test/parity/interfaces/mapping-v9.9.9.json", `{"mappings":[
		{"id":"pkg:ai/index.ts#listed","disposition":"ported","rationale":"Reviewed by hand."},
		{"id":"pkg:ai/index.ts#pending","disposition":"pending","rationale":""},
		{"id":"pkg:ai/index.ts#derived","disposition":"ported","rationale":"Derived by the interface gap detector: x."},
		{"id":"pkg:ai/index.ts#reviewed","disposition":"ported","rationale":"Reviewed by hand."},
		{"id":"pkg:ai/index.ts#out","disposition":"designed-out","rationale":"D2."}]}`)
	writeFile(t, root, "test/parity/interface-closure/gaps/interface-gaps-v9.9.9.tsv", "pkg:ai/index.ts#listed\tai\tsignature-mismatch\n")
	pl, err := loadPinLedger(root, "9.9.9", true, newSymIndex(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"pkg:ai/index.ts#listed":   {StatusGap, "signature-mismatch"},
		"pkg:ai/index.ts#pending":  {StatusGap, "pending"},
		"pkg:ai/index.ts#derived":  {StatusDerived, ""},
		"pkg:ai/index.ts#reviewed": {StatusPorted, ""},
		"pkg:ai/index.ts#out":      {StatusDesignedOut, ""},
	}
	if len(pl.Rows) != len(want) {
		t.Fatalf("%d rows, want %d", len(pl.Rows), len(want))
	}
	for _, r := range pl.Rows {
		if got := [2]string{r.Status, r.GapReason}; got != want[r.ID] {
			t.Errorf("%s: status and gap reason %v, want %v", r.ID, got, want[r.ID])
		}
	}
	if pl.GapBaseline["ai"] != 1 || pl.GapBaselineRows != 1 {
		t.Errorf("gap baseline per package %v, rows %d", pl.GapBaseline, pl.GapBaselineRows)
	}
}

func TestPinOrderingAndTargets(t *testing.T) {
	vs := []string{"1.1.0+42a3497d", "1.0.4", "1.1.0", "0.87.1", "1.1.1"}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
	if strings.Join(vs, " ") != "0.87.1 1.0.4 1.1.0 1.1.0+42a3497d 1.1.1" {
		t.Errorf("order: %v", vs)
	}
	got := pinTargets("1.1.0", vs)
	if strings.Join(got, " ") != "1.1.0 1.1.0+42a3497d 1.1.1" {
		t.Errorf("targets: %v", got)
	}
}

func TestParseTotalsLogKeepsOnlyNumbersAndLabels(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "totals.log", strings.Join([]string{
		"TOTALS 5862/12234 (ported/total; +2 designed-out) some-lane 54220fb62 open rows per package: agent=107",
		"NOTE something else",
		"2026-10-10T17:29:25Z TOTALS[Pi 1.1.0; some-lane 6c0a6b1920] interface-gaps: 3 gaps, baseline 3, no new gap",
		"TOTALS[Pi 1.1.0; some-lane cf9acba42f] GAP 7 of 12324 (= -int); unproven-behaviour telemetry 13",
	}, "\n")+"\n")
	tr := parseTotalsLog(filepath.Join(dir, "totals.log"))
	if tr == nil || len(tr.Points) != 3 {
		t.Fatalf("%+v", tr)
	}
	want := []int{12234 - 5862, 3, 7}
	for i, p := range tr.Points {
		if p.Value != want[i] {
			t.Errorf("point %d: %d, want %d", i, p.Value, want[i])
		}
		if strings.Contains(p.Label, "lane") || strings.Contains(p.Label, "6c0a") {
			t.Errorf("label %q carries lane or commit data", p.Label)
		}
	}
	if tr.Points[1].Label != "2026-10-10T17:29 Pi 1.1.0" {
		t.Errorf("label: %q", tr.Points[1].Label)
	}
	if parseTotalsLog(filepath.Join(dir, "missing.log")) != nil {
		t.Error("a missing log gives no trend")
	}
}

func TestScrubRemovesTrailersAndAddresses(t *testing.T) {
	got := scrub("The cause. Signed-off-by: A Person <a.person@example.test>\nCo-authored-by: B <b@example.test>")
	if got != "The cause." {
		t.Errorf("%q", got)
	}
	if got := scrub("mail a@b.example now"); strings.Contains(got, "@") {
		t.Errorf("%q", got)
	}
}

func TestEvaluateBarJudgesEachItemFromData(t *testing.T) {
	cell := func(cold, warm, cpu, rss float64) ArmCell {
		return ArmCell{Samples: 3, ColdMs: cold, WarmMs: warm, CPUSec: cpu, RSSMB: rss}
	}
	bar := DurableBar{Sizes: []int{50, 1000, 3500}, Baseline: BaselineInfo{Path: "b.toml", Entries: 0}, Arms: []DurableArm{
		{Name: piArm, Pi: true, Cells: map[int]ArmCell{50: cell(100, 50, 1, 90), 1000: cell(300, 200, 3, 200), 3500: cell(900, 700, 10, 290)}},
		{Name: headlineArm, Headline: true, Cells: map[int]ArmCell{50: cell(60, 90, 1.2, 38), 1000: cell(200, 190, 2.9, 50), 3500: cell(480, 360, 6, 72)}},
	}}
	items := evaluateBar(&bar)
	if len(items) != 4 {
		t.Fatalf("%d items", len(items))
	}
	if !items[0].Met {
		t.Error("item 1: an empty baseline is met")
	}
	if items[1].Met {
		t.Error("item 2: PiG loses warm wall and CPU at 50 turns, so the item is not met")
	}
	lost := 0
	for _, c := range items[1].Cells {
		if !c.Met {
			lost++
			if c.Size != 50 {
				t.Errorf("only the 50-turn cells lose: %+v", c)
			}
		}
	}
	if lost != 2 {
		t.Errorf("%d lost cells, want 2 (warm wall, CPU)", lost)
	}
	if items[2].Met {
		t.Error("item 3: 22 MB over 2,500 turns is not flat")
	}
	if items[3].Met {
		t.Error("item 4: no cluster result is committed")
	}
	bar.Baseline.Entries = 2
	bar.Arms[1].Cells[3500] = cell(480, 360, 6, 50.1)
	items = evaluateBar(&bar)
	if items[0].Met || !items[2].Met {
		t.Errorf("a baseline entry fails item 1; a flat curve meets item 3: %v %v", items[0].Met, items[2].Met)
	}
}

func TestLoadDurableBarReadsSamplesAndMedians(t *testing.T) {
	dir := t.TempDir()
	row := func(turns int, open float64, first, rest float64) string {
		turn := []float64{first}
		for range 9 {
			turn = append(turn, rest)
		}
		b, _ := json.Marshal(map[string]any{"turns": turns, "open": open, "turn": turn, "rss": 10, "peakRss": 12, "cpuSeconds": 2})
		return string(b) + "\n"
	}
	writeFile(t, dir, "bench/durableperf/results/"+piArm+".jsonl", row(50, 5, 70, 40)+row(50, 7, 90, 44)+row(50, 6, 80, 42)+"not json\n")
	writeFile(t, dir, "durable/contract/baseline.toml", "# empty\n")
	bar := loadDurableBar(dir)
	c := bar.arm(piArm).Cells[50]
	if c.Samples != 3 || c.ColdMs != 86 || c.WarmMs != 42 || c.RSSMB != 12 || c.CPUSec != 2 {
		t.Errorf("%+v", c)
	}
	if bar.Baseline.Entries != 0 {
		t.Errorf("baseline: %+v", bar.Baseline)
	}
	writeFile(t, dir, "durable/contract/baseline.toml", "[[row]]\nid = \"a\"\n[[row]]\nid = \"b\"\n")
	if n := loadBaselineEntries(dir).Entries; n != 2 {
		t.Errorf("two entries, got %d", n)
	}
}

func TestLoadMatricesKeepsProvisionalFlagsAndCannotRunCells(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bench/compare/results/m.json", `{"title":"T","provisional":true,"machine":"M","arms":["a","b"],"rows":[
		{"id":"r1","scenario":"startup","unit":"ms","lowerIsBetter":true,"provisional":true,"samples":"raw/r1.jsonl","cells":{"a":{"median":10,"min":9,"max":12,"n":5},"b":{"median":20,"n":5}}},
		{"id":"r2","scenario":"print","unit":"ms","lowerIsBetter":true,"cells":{"a":{"median":1},"b":{"cannotRun":"no print mode"}}}]}`)
	ms := loadMatrices(dir)
	if len(ms) != 1 || !ms[0].Provisional || len(ms[0].Rows) != 2 {
		t.Fatalf("%+v", ms)
	}
	rep := &Report{Evals: Evals{Matrices: ms}}
	v := buildEvalsView(rep, func(string) string { return "" })
	if len(v.Matrices) != 1 {
		t.Fatal("no matrix view")
	}
	r1, r2 := v.Matrices[0].Rows[0], v.Matrices[0].Rows[1]
	if !r1.Cells[0].Best || r1.Cells[1].Best || r1.Cells[0].Text != "10.0 (9.00\u201312.0, n=5)" {
		t.Errorf("row 1: %+v", r1.Cells)
	}
	if r2.Cells[1].Cannot != "no print mode" || !r2.Cells[0].Best {
		t.Errorf("row 2: %+v", r2.Cells)
	}
}

// luminance and ratio follow WCAG 2.1.
func luminance(hex string) float64 {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	n, _ := strconv.ParseUint(hex, 16, 32)
	ch := func(v uint64) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(n>>16&255) + 0.7152*ch(n>>8&255) + 0.0722*ch(n&255)
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Text colors keep WCAG AA contrast (4.5:1) on the surfaces they sit on, in both themes.
func TestColorTokensMeetContrast(t *testing.T) {
	b, err := assets.ReadFile("assets/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	block := func(selector string) map[string]string {
		i := strings.Index(css, selector+"{")
		if i < 0 {
			t.Fatalf("no %s block", selector)
		}
		j := strings.Index(css[i:], "}")
		out := map[string]string{}
		for _, m := range regexp.MustCompile(`--([a-z-]+):(#[0-9a-fA-F]{3,6})\b`).FindAllStringSubmatch(css[i:i+j], -1) {
			out[m[1]] = m[2]
		}
		return out
	}
	light := block(":root")
	dark := map[string]string{}
	maps.Copy(dark, light)
	maps.Copy(dark, block(":root[data-theme=dark]"))
	// The system-dark rule repeats the dark tokens; they must agree.
	sys := block(":root:not([data-theme=light])")
	for k, v := range sys {
		if dark[k] != v {
			t.Errorf("--%s is %s in the dark theme and %s under prefers-color-scheme", k, dark[k], v)
		}
	}
	for name, theme := range map[string]map[string]string{"light": light, "dark": dark} {
		for _, fg := range []string{"ink", "subtle", "green", "ok", "info", "violet", "bad", "warn"} {
			for _, bg := range []string{"page", "surface", "soft"} {
				if r := contrast(theme[fg], theme[bg]); r < 4.5 {
					t.Errorf("%s theme: --%s on --%s is %.2f:1, below 4.5:1", name, fg, bg, r)
				}
			}
		}
		for _, fg := range []string{"code-ink", "code-mute", "hl-k", "hl-s", "hl-c", "hl-m", "hl-f", "hl-t", "hl-o"} {
			if r := contrast(theme[fg], theme["code-bg"]); r < 4.5 {
				t.Errorf("%s theme: code token --%s on --code-bg is %.2f:1", name, fg, r)
			}
		}
	}
}

func TestDownsampleKeepsEndsAndOrder(t *testing.T) {
	var p []TrendPoint
	for i := range 1000 {
		p = append(p, TrendPoint{Value: i})
	}
	got := downsample(p, 120)
	if len(got) != 120 || got[0].Value != 0 || got[119].Value != 999 {
		t.Fatalf("%d points from %d to %d", len(got), got[0].Value, got[len(got)-1].Value)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Value <= got[i-1].Value {
			t.Fatalf("not increasing at %d", i)
		}
	}
	if short := downsample(p[:5], 120); len(short) != 5 {
		t.Errorf("a short series is kept whole: %d", len(short))
	}
}

func TestDedentRemovesTheSharedIndentOnly(t *testing.T) {
	got := dedent([]string{"\t\t\tif x {", "\t\t\t\ty()", "", "\t\t\t}"})
	if got != "if x {\n\ty()\n\n}" {
		t.Errorf("%q", got)
	}
	if got := dedent([]string{"a", "\tb"}); got != "a\n\tb" {
		t.Errorf("a line at the left edge keeps the rest as is: %q", got)
	}
}

func TestSampleLinkerCopiesRepositoryFilesOnly(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	writeFile(t, root, "bench/r/raw.jsonl", "{}\n")
	link := newSampleLinker(root, out)
	if got := link("bench/r/raw.jsonl"); got != "samples/bench/r/raw.jsonl" {
		t.Fatalf("link %q", got)
	}
	if b, err := os.ReadFile(filepath.Join(out, "samples", "bench", "r", "raw.jsonl")); err != nil || string(b) != "{}\n" {
		t.Errorf("the sample is copied: %q %v", b, err)
	}
	for _, bad := range []string{"", "../escape", "/etc/passwd", "bench/r/none.jsonl", "bench/../../x"} {
		if got := link(bad); got != "" {
			t.Errorf("%q gives link %q", bad, got)
		}
	}
}
