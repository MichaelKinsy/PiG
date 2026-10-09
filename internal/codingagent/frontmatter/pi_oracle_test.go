package frontmatter

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/yaml12"
)

// piYAMLCorpus is frontmatter YAML that Pi's YAML library (eemeli/yaml) reads and writes in many shapes: values, scalar types, and the exact wording of each syntax error.
var piYAMLCorpus = []string{
	"a: !!binary aGVsbG8=",
	"a: !!timestamp 2001-12-14",
	"a: !!timestamp 2001-12-14T21:59:43.10-05:00",
	"a: !!timestamp nope",
	"a: !!set {x, y}",
	"a: !!set {x: 1}",
	"a: !!omap [x: 1, y: 2]",
	"a: !!omap [x: 1, x: 2]",
	"a: !!pairs [x: 1, x: 2]",
	"!!merge <<: {x: 1}\nb: 2",
	"x: 1\n!!merge <<: [{x: 2, y: 3}, {z: 4}]\n",
	"!!merge <<: 5",
	"a: !!merge x",
	"? [a, b]\n: v",
	"? {a: 1}\n: v",
	"? [a, b]\n: 1\n? [a, b]\n: 2",
	"&k [a]: 1\n*k : 2",
	"? !!set {a}\n: v",
	"? [!!timestamp 2001-12-14, 1.50]\n: v",
	"? [a, b # c\n]\n: v",
	"? [\"long text long text long text long text long text long text long text long text long text\"]\n: v",
	"- a\n- b",
	"just text",
	"5",
	"~",
	"!!set {a}",
	"description: !!str\nname: !!null",
	"tools: !!seq [a, b]",
	"tools: !!map {a: b}",
	"description: [unclosed", "description: \"unclosed", "description: 'unclosed", "description: {a: 1", "a: 1\na: 2", "\tdescription: x", "a:\n\t- b",
	"description: @bad", "description: `bad", "description: %bad", "description: |\n  ok\n bad", "a: b: c", "- a\nb: c", "a: [1, 2]]", "a: &x\nb: *y",
	"a: !!int abc", "a: b\n  c: d", "key: value: other", "description: Use when: a thing", "a: 'it''s'", "a: \"\\q\"", "? a\n: b", "a: -", ": x", "a: - b",
	"a:\n  - b\n c: d", "a: |\n x\n\n y: z", "#only comment", "a: 1\n---\nb: 2", "a: 1\n... \nb", "[a, b", "- - -",
	"a: 0x1F\nb: 1_000\nc: .inf\nd: 2001-12-14\ne: yes\nf: ~\ng: 1e3\nh: 0o17\ni: 017", "k: >\n  folded\n  text\nl: |-\n  lit", "a: [x, y, ]\nb: {c: d, }",
	"a: |\n  x", "a: |\n  x\n", "a: |+\n  x", "a: |+\n  x\n\n", "a: |-\n  x", "a: >\n  x\n  y", "a: >+\n  x", "a: >-\n  x\n", "a: |\n  x\n\n\n", "a: |2\n   x", "b: 1\na: |\n  x\n\n",
	"'a': \"b\"\n? c\n: d", "a: b #c\nd: e#f", "1: one\ntrue: t\nnull: n", "description: Plain text\nargument-hint: <file> [--flag]\n", "description: \"quoted: colon\"\nname: n",
	"1.5: a\n2: b\nname: n\ndescription: d", "a:\n  1: x\n  false: y", "~: z\nb: 1", "a:\n  - 1: x\n  - b: y", "1e3: big\n-0: z",
}

func TestParseMatchesPi(t *testing.T) {
	var want []struct {
		Err string `json:"err"`
		Fm  any    `json:"fm"`
	}
	pioracle.Run(t, `
const fm = await load("pi-coding-agent/utils/frontmatter.js");
emit(input.map((y) => { try { const r = fm.parseFrontmatter("---\n" + y + "\n---\nbody"); return { err: "", fm: r.frontmatter }; } catch (e) { return { err: e.message, fm: null }; } }));`, piYAMLCorpus, &want)
	for i, y := range piYAMLCorpus {
		doc := Parse("---\n" + y + "\n---\nbody")
		got, fm := "", []byte("null")
		if doc.Err != nil {
			got = doc.Err.Error()
		} else if encoded, err := json.Marshal(jsonSafe(doc.Value)); err == nil {
			fm = encoded
		} else {
			fm = []byte(err.Error())
		}
		wantFM, _ := json.Marshal(want[i].Fm)
		if got != want[i].Err || (got == "" && string(fm) != string(wantFM)) {
			t.Errorf("YAML %q:\n  Pig err=%q fm=%s\n  Pi  err=%q fm=%s", y, got, fm, want[i].Err, wantFM)
		}
	}
}

// jsonSafe maps the values JSON.stringify writes specially: NaN and the infinities are null, a Date is its ISO string (null when invalid), a byte buffer an object of its indices, a Set or Map an empty object, and a Symbol null (omitted from an object).
func jsonSafe(v any) any {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil
		}
	case yaml12.Date:
		if math.IsNaN(t.MS) {
			return nil
		}
		return time.UnixMilli(int64(t.MS)).UTC().Format("2006-01-02T15:04:05.000Z")
	case []byte:
		out := make(map[string]any, len(t))
		for i, b := range t {
			out[strconv.Itoa(i)] = float64(b)
		}
		return out
	case yaml12.Set, yaml12.Map:
		return map[string]any{}
	case yaml12.Symbol:
		return nil
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = jsonSafe(x)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if _, isSymbol := x.(yaml12.Symbol); isSymbol {
				continue
			}
			out[k] = jsonSafe(x)
		}
		return out
	}
	return v
}
