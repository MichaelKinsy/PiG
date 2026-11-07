package yaml12

// The binary links two YAML parsers: this package, which is the Pi-faithful YAML 1.2 core-schema parser (frontmatter and the extension host) and go.yaml.in/yaml/v3, which reads and writes Piglets, Compose files and the Piglet closure. This test states how far the two read the same text. It parses every document of the corpus with both and writes each difference to testdata/v3diff/differences.txt, so a change that replaces one parser by the other shows the exact documents whose meaning moves. Run it with -update-v3diff to rewrite the file after a reviewed change.

import (
	"encoding/json"
	"flag"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	v3 "go.yaml.in/yaml/v3"
)

var updateV3Diff = flag.Bool("update-v3diff", false, "rewrite testdata/v3diff/differences.txt")

const v3diffGolden = "testdata/v3diff/differences.txt"

func v3diffNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	case f == math.Trunc(f) && math.Abs(f) < 1e21:
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// v3diffKey is the property name JavaScript gives a scalar key, which is what the yaml12 side builds.
func v3diffKey(k any) string {
	switch t := k.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case float64:
		return v3diffNumber(t)
	}
	return "<" + strconv.Quote(strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strconvQuoteAny(k), "\n", " "), "\t", " "))) + ">"
}

func strconvQuoteAny(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func v3diffObject(keys []string, values []string) string {
	idx := make([]int, len(keys))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		if keys[idx[a]] != keys[idx[b]] {
			return keys[idx[a]] < keys[idx[b]]
		}
		return values[idx[a]] < values[idx[b]]
	})
	var sb strings.Builder
	sb.WriteByte('{')
	for n, i := range idx {
		if n > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Quote(keys[i]))
		sb.WriteByte(':')
		sb.WriteString(values[i])
	}
	sb.WriteByte('}')
	return sb.String()
}

// v3diffNodes counts the values one rendering has visited: an alias bomb or a cyclic alias expands to a tree far larger than its text, so a rendering gives up past a bound.
var v3diffNodes int

// v3diffNormV3 renders a value yaml.Unmarshal built into an any, in a form the yaml12 side can match.
func v3diffNormV3(v any, depth int) string {
	v3diffNodes++
	if depth > 40 || v3diffNodes > 20000 {
		return "<deep>"
	}
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint64:
		return v3diffNumber(float64(t))
	case float64:
		return v3diffNumber(t)
	case string:
		return strconv.Quote(t)
	case time.Time:
		return "timestamp(" + t.UTC().Format(time.RFC3339Nano) + ")"
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = v3diffNormV3(e, depth+1)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(t))
		values := make([]string, 0, len(t))
		for k, e := range t {
			keys = append(keys, k)
			values = append(values, v3diffNormV3(e, depth+1))
		}
		return v3diffObject(keys, values)
	case map[any]any:
		keys := make([]string, 0, len(t))
		values := make([]string, 0, len(t))
		for k, e := range t {
			keys = append(keys, v3diffKey(k))
			values = append(values, v3diffNormV3(e, depth+1))
		}
		return v3diffObject(keys, values)
	}
	return "<" + strconv.Quote(strconvQuoteAny(v)) + ">"
}

// v3diffNormY12 renders a value ParseOrdered built.
func v3diffNormY12(v any, depth int) string {
	v3diffNodes++
	if depth > 40 || v3diffNodes > 20000 {
		return "<deep>"
	}
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return v3diffNumber(t)
	case string:
		return strconv.Quote(t)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = v3diffNormY12(e, depth+1)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *Object:
		values := make([]string, len(t.Keys))
		for i, k := range t.Keys {
			values[i] = v3diffNormY12(t.Values[k], depth+1)
		}
		return v3diffObject(t.Keys, values)
	case Date:
		return "date(" + v3diffNumber(t.MS) + ")"
	case Symbol:
		return "symbol(" + strconv.Quote(string(t)) + ")"
	}
	return "<" + strconv.Quote(strconvQuoteAny(v)) + ">"
}

type v3diffResult struct {
	text string
	err  bool
}

func v3diffRunV3(src string) (out v3diffResult) {
	v3diffNodes = 0
	defer func() {
		if r := recover(); r != nil {
			out = v3diffResult{text: "panic", err: true}
		}
	}()
	var v any
	if err := v3.Unmarshal([]byte(src), &v); err != nil {
		return v3diffResult{text: "error", err: true}
	}
	return v3diffResult{text: v3diffNormV3(v, 0)}
}

func v3diffRunY12(src string) (out v3diffResult) {
	v3diffNodes = 0
	defer func() {
		if r := recover(); r != nil {
			out = v3diffResult{text: "panic", err: true}
		}
	}()
	v, err := ParseOrdered(src)
	if err != nil {
		return v3diffResult{text: "error", err: true}
	}
	return v3diffResult{text: v3diffNormY12(v, 0)}
}

type v3diffCase struct{ group, name, src string }

func v3diffQuote(s string) string {
	q := strconv.Quote(s)
	if len(q) > 160 {
		q = q[:157] + `..."`
	}
	return q
}

// v3diffEdgeCases are the spellings and structures where YAML 1.1 and YAML 1.2 readers part ways, and the structures a Piglet or Compose file uses.
func v3diffEdgeCases() []string {
	return []string{
		// Bool, null, number and timestamp spellings.
		"v: yes", "v: No", "v: on", "v: OFF", "v: y", "v: n", "v: True", "v: TRUE", "v: tRUE", "v: Null", "v: ~", "v:", "v: \"\"",
		"v: 010", "v: 0o10", "v: 0x1F", "v: 0b11", "v: 1_000", "v: +1", "v: -0", "v: .5", "v: 5.", "v: 1e3", "v: 1E3", "v: .inf", "v: -.Inf", "v: .NaN",
		"v: 0.10", "v: 1.10", "v: 1.0", "v: 9007199254740993", "v: 99999999999999999999", "v: 1e400", "v: 0xFFFFFFFFFFFFFFFFFF", "v: 190:20:30", "v: 12:30", "v: 3.14.15",
		"v: 2001-12-14", "v: 2001-12-14t21:59:43.10-05:00", "v: 2001-12-14 21:59:43.10 -5", "v: 2001-12-14T21:59:43Z", "v: 2001-13-45", "v: '2001-12-14'", "v: !!timestamp 2001-12-14", "v: !!binary aGk=", "v: !!set {a, b}", "v: !!omap [a: 1]",
		"v: =", "v: <<", "v: !!str 1", "v: !!int \"3\"", "v: !!float 1", "v: !!bool yes",
		// Anchors, aliases, merge keys.
		"a: &x {b: 1}\nc: *x", "a: &x {b: 1}\nc:\n  <<: *x", "a: &x {b: 1}\nc:\n  <<: *x\n  b: 2", "a: &x {b: 1}\nc:\n  b: 2\n  <<: *x", "a: &x {b: 1}\nc:\n  <<: [*x, {d: 3}]", "<<: {a: 1}\nb: 2", "a: {<<: 1}", "a: &x 1\nb: *x", "a: *x", "&a [*a]", "a: &x [1, 2]\nb: &y [*x, *x]\nc: [*y, *y]", "a: !!merge <<",
		"services:\n  a: &base\n    build: ./a\n  b:\n    <<: *base\n    image: x",
		// Keys.
		"a: 1\na: 2", "a: 1\nb: 2\na: 3", "{a: 1, a: 2}", "? a\n: 1\n? a\n: 2", "1: a\n1.0: b", "1: a\n\"1\": b", "true: a\n\"true\": b", "null: a\n~: b", "'': a\n'': b", "[a, b]: c", "{a: b}: c", "? [a, b]\n: c", "1: one\n2: two", "0x10: a\n16: b", "a: 1\n<<: {a: 2}",
		// Indentation and tabs.
		"a:\n  b: 1\n  c: 2", "a:\n\tb: 1", "a: 1\n\tb: 2", "a:\n  - b\n  - c", "a:\n- b\n- c", "a:\n - b\n  - c", "a: b\n c: d", "a:\n  b: 1\n c: 2", "- a\n - b", "a: \tb", "a:\tb", "- \ta", "a: [\tb]", "a:\n  b: |\n\tx",
		// Flow style.
		"a: [1, 2, ]", "a: {b: 1, }", "a: [1,2,3]", "{a: 1, b: 2}", "[a, b: c]", "[a: b]", "a: [b: c, d]", "{a, b: , c: d}", "{? a : b}", "a: {b}", "a: [", "a: {", "a: [b]]", "a: {b: [c: d]}", "[a,\nb]", "a: [x,\n  y]", "a: {x: 1,\n  y: 2}", "{\"a\":b}", "{a:b}", "[a:b]", "a: [-]", "a: [- b]", "a: [b, #c\n c]",
		// Plain scalars.
		"a: b: c", "a: b:c", "a:b", "a: - b", "a: -b", "a: ?b", "a: :b", "a: b #c", "a: b#c", "a: @b", "a: `b", "a: %b", "a: |b", "a: >b", "a: !b", "a: &b", "a: *b", "a: 'b' c", "a: \"b\" c", "a: [b] c",
		"a: multi\n  line\n  plain", "a: multi\n\n  line", "a: x\n  # c\n  y", "a: 'multi\n  line'", "a: \"multi\n  line\"", "a: \"x\\\n  y\"", "a: \"\\q\"", "a: \"\\x41\\u0042\\U00000043\"", "a: \"\\ud800\"", "a: \"\\/\"", "a: \"\\_\\N\\L\\P\"", "a: 'it''s'", "a: \"tab\\there\"",
		// Block scalars.
		"a: |\n  x\n  y", "a: |-\n  x\n", "a: |+\n  x\n\n\n", "a: >\n  x\n  y\n\n  z", "a: >-\n  x\n", "a: |2\n   x", "a: |\n\n  x", "a: |\n  x\n   y", "a: >\n  x\n   y\n  z", "a: |1\n  x", "a: |\nx", "a: | # c\n  x", "a: |x\n  y", "a: |\n\tx", "- |\n x\n- >\n y",
		// Documents, directives, comments, BOM.
		"a: 1\n---\nb: 2", "---\na: 1", "--- a\n--- b", "a: 1\n...\nb", "a: 1\n...", "---\n---\n", "", "\n", "# only a comment", "---", "...", "%YAML 1.2\n---\na: 1", "%YAML 1.1\n---\na: 1", "%YAML 2.0\n---\na: 1", "%TAG !e! tag:e.com,2000:\n---\n!e!x a", "\ufeffa: 1", "a: 1\n\ufeffb: 2", "a: 1 # c\nb: 2#d",
		"%YAML 1.2\n%YAML 1.2\n---\na", "a: 1\n%YAML 1.2\n---\nb: 2",
		// Tags.
		"a: !foo bar", "a: !!foo bar", "a: !<tag:yaml.org,2002:str> 1", "a: !!str", "a: &x", "&x a: 1", "!!map a: 1", "a: !!map {b: 1}", "a: !!seq [1]", "a: !!null ''", "a: !!int abc", "a: !!float abc", "a: !!bool maybe", "a: !!map [1]", "a: !!seq {b: 1}", "a: !e!x b",
		// Complex keys and long input.
		"? |\n  x\n: y", "? - a\n  - b\n: c", ": x", "? a\n: b", "? a", "-", "- - a\n  - b", "- a: 1\n  b: 2\n- c: 3",
		strings.Repeat("k", 1023) + ": v", strings.Repeat("k", 1024) + ": v", strings.Repeat("k", 1025) + ": v", "[" + strings.Repeat("k", 1024) + ": v]", "a: " + strings.Repeat("word ", 4000), "a: \"" + strings.Repeat("é", 5000) + "\"",
		strings.Repeat("a:\n  ", 200) + "b: 1", strings.Repeat("[", 200) + strings.Repeat("]", 200), strings.Repeat("- ", 60) + "x",
		// Unicode and control characters.
		"é: ü\n😀: 😀 x", "a: \"😀\\u2028\"", "a: \u2028", "a: \u0085", "a: \x00", "a: \x07", "a: \u00a0", "a:\u00a0b", "a: \xff", "a: \"\xc3\"", "a: \ud7ff\ue000", "a: \U0010ffff", "a: \ufffe", "\u00a0a: b", "a: \u200bb", "a:\r\n  b: 1\r\n", "a: 1\rb: 2",
		// Piglet and Compose shapes.
		"name: x\nextensions:\n  - a\n  - name: b\n    origins: [c]\n", "name: x\nextends:\n  version: 1.10\n", "name: x\nextends:\n  version: \"1.10\"\n", "name: 123\n", "name: 1e3\n", "name: true\n", "name: null\n", "name: x\ncontextFiles: yes\n", "name: x\nagentEnv:\n  workspace:\n    readonly: yes\n", "name: x\nagentEnv:\n  mounts:\n    - source: a\n      target: b\n      readonly: on\n",
		"services:\n  web:\n    build: .\n    ports:\n      - 80:80\n      - \"22:22\"\n      - 2222:22\n", "services:\n  web:\n    build:\n      context: ./web\n    environment:\n      - A=1\n      - B\n    command: [\"a\", \"b\"]\n", "x-common: &c\n  build: ./x\nservices:\n  a:\n    <<: *c\n",
		"version: '3'\nservices:\n  a:\n    build: ../escape\n    deploy: {resources: {limits: {cpus: 0.50}}}\n    ports: [8080:80, 22:22]\n",
	}
}

// v3diffScalars are the scalar spellings the typed-decode table covers.
func v3diffScalars() []string {
	return []string{"yes", "no", "Yes", "NO", "on", "off", "On", "y", "n", "true", "True", "TRUE", "tRUE", "false", "FALSE", "null", "Null", "~", "", "010", "0o10", "0x10", "0b10", "1_000", "+1", "-1", "00", "0.0", "1.0", "1.10", ".5", "5.", "1e3", ".inf", ".NaN", "12:30", "190:20:30", "2001-01-01", "9007199254740993", "9223372036854775808", "18446744073709551616", "1e400", "\"1.10\"", "'yes'", "!!str 5", "!!int \"5\"", "!!bool yes", "!!float 5"}
}

func v3diffTyped(scalar string) []string {
	src := "v: " + scalar
	var rows []string
	type outcome struct{ kind, v3, y12 string }
	var results []outcome
	var b struct {
		V bool `yaml:"v"`
	}
	var i struct {
		V int `yaml:"v"`
	}
	var f struct {
		V float64 `yaml:"v"`
	}
	var s struct {
		V string `yaml:"v"`
	}
	res := func(err error, v string) string {
		if err != nil {
			return "ERR"
		}
		return v
	}
	jsValue, jsErr := ParseOrdered(src)
	var js any
	if obj, ok := jsValue.(*Object); ok && jsErr == nil {
		js = obj.Values["v"]
	}
	y12 := func(kind string) string {
		if jsErr != nil {
			return "ERR"
		}
		switch kind {
		case "bool":
			if t, ok := js.(bool); ok {
				return strconv.FormatBool(t)
			}
		case "int":
			if t, ok := js.(float64); ok && t == math.Trunc(t) && math.Abs(t) < 1<<63 {
				return strconv.FormatInt(int64(t), 10)
			}
		case "float64":
			if t, ok := js.(float64); ok {
				return v3diffNumber(t)
			}
		case "string":
			if t, ok := js.(string); ok {
				return strconv.Quote(t)
			}
		}
		return "ERR"
	}
	results = append(results,
		outcome{"bool", res(v3.Unmarshal([]byte(src), &b), strconv.FormatBool(b.V)), y12("bool")},
		outcome{"int", res(v3.Unmarshal([]byte(src), &i), strconv.Itoa(i.V)), y12("int")},
		outcome{"float64", res(v3.Unmarshal([]byte(src), &f), v3diffNumber(f.V)), y12("float64")},
		outcome{"string", res(v3.Unmarshal([]byte(src), &s), strconv.Quote(s.V)), y12("string")},
	)
	for _, r := range results {
		if r.v3 != r.y12 {
			rows = append(rows, "typed\t"+v3diffQuote(src)+"\tinto "+r.kind+"\tv3="+r.v3+"\tyaml12="+r.y12+"\tyaml12 value="+v3diffNormY12(js, 0))
		}
	}
	return rows
}

func v3diffRepoFiles(t *testing.T) []v3diffCase {
	t.Helper()
	root := filepath.Join("..", "..")
	var cases []v3diffCase
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".upstream", "node_modules", "target", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// A Windows checkout may rewrite line endings; the corpus is the text as committed.
		cases = append(cases, v3diffCase{group: "file", name: filepath.ToSlash(rel), src: strings.ReplaceAll(string(data), "\r\n", "\n")})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestV3DifferentialCorpus(t *testing.T) {
	var cases []v3diffCase
	cases = append(cases, v3diffRepoFiles(t)...)
	data, err := os.ReadFile("testdata/v3diff/piglet-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var harvested []string
	if err := json.Unmarshal(data, &harvested); err != nil {
		t.Fatal(err)
	}
	for _, src := range harvested {
		cases = append(cases, v3diffCase{group: "piglet-tests", name: v3diffQuote(src), src: src})
	}
	for _, src := range v3diffEdgeCases() {
		cases = append(cases, v3diffCase{group: "edge", name: v3diffQuote(src), src: src})
	}
	oracle := yamlCorpus()
	for _, src := range oracle {
		cases = append(cases, v3diffCase{group: "oracle", name: v3diffQuote(src), src: src})
	}

	groupTotal := map[string]int{}
	groupSame := map[string]int{}
	kinds := map[string]map[string]int{}
	var rows []string
	for _, c := range cases {
		groupTotal[c.group]++
		a, b := v3diffRunV3(c.src), v3diffRunY12(c.src)
		var kind string
		switch {
		case a.err && b.err:
			kind = "both-reject"
		case a.text == b.text:
			kind = "same"
		case a.err:
			kind = "v3-rejects"
		case b.err:
			kind = "yaml12-rejects"
		default:
			kind = "meaning"
		}
		if kind == "same" || kind == "both-reject" {
			groupSame[c.group]++
			continue
		}
		if kinds[c.group] == nil {
			kinds[c.group] = map[string]int{}
		}
		kinds[c.group][kind]++
		// The oracle corpus is a stress set for the Pi parser, not text a Piglet author writes: keep its counts, list the others one by one.
		if c.group == "oracle" {
			continue
		}
		rows = append(rows, c.group+"\t"+kind+"\t"+c.name+"\tv3="+v3diffQuote(a.text)+"\tyaml12="+v3diffQuote(b.text))
	}
	for _, scalar := range v3diffScalars() {
		rows = append(rows, v3diffTyped(scalar)...)
	}
	sort.Strings(rows)

	var sb strings.Builder
	sb.WriteString("# Differences between go.yaml.in/yaml/v3 and internal/yaml12 on the corpus of v3_differential_test.go. Columns: group, kind, document, v3 reading, yaml12 reading.\n")
	sb.WriteString("# Readings are the document as a JSON-like value (v3: yaml.Unmarshal into an any; yaml12: ParseOrdered); keys are sorted. kind is meaning (both accept, different value), v3-rejects, or yaml12-rejects.\n")
	groups := make([]string, 0, len(groupTotal))
	for g := range groupTotal {
		groups = append(groups, g)
	}
	slices.Sort(groups)
	for _, g := range groups {
		sb.WriteString("# group " + g + ": " + strconv.Itoa(groupTotal[g]) + " documents, " + strconv.Itoa(groupSame[g]) + " read the same or rejected by both")
		ks := make([]string, 0, len(kinds[g]))
		for k := range kinds[g] {
			ks = append(ks, k)
		}
		slices.Sort(ks)
		for _, k := range ks {
			sb.WriteString(", " + k + " " + strconv.Itoa(kinds[g][k]))
		}
		sb.WriteString("\n")
	}
	for _, row := range rows {
		sb.WriteString(row + "\n")
	}
	got := sb.String()
	if *updateV3Diff {
		if err := os.WriteFile(v3diffGolden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(v3diffGolden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("the two parsers' readings of the corpus changed; review the difference and run go test ./internal/yaml12 -run TestV3DifferentialCorpus -update-v3diff\n%s", firstDiffLines(string(want), got))
	}
}

// firstDiffLines names the first lines where want and got part, so a failure shows what moved.
func firstDiffLines(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out []string
	for i := 0; i < max(len(w), len(g)) && len(out) < 12; i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			out = append(out, "- "+a, "+ "+b)
		}
	}
	return strings.Join(out, "\n")
}
