package jsonschema

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// PiG fork: each draft's metaschema compiles on its first use, not every draft's at package initialisation or on the first Compiler.

var allDrafts = []*Draft{Draft4, Draft6, Draft7, Draft2019, Draft2020}

// metaschemasCompiledAtStart records whether any draft's metaschema existed before the tests ran.
var metaschemasCompiledAtStart bool

func TestMain(m *testing.M) {
	for _, d := range allDrafts {
		metaschemasCompiledAtStart = metaschemasCompiledAtStart || d.sch != nil
	}
	os.Exit(m.Run())
}

// freshChildEnv names the test a child process runs; the child is this test binary, so it inherits -race.
const freshChildEnv = "PIG_JSONSCHEMA_FRESH_CHILD"

// freshChild reports whether this process is the child started for the named test.
func freshChild(name string) bool { return os.Getenv(freshChildEnv) == name }

// runFresh runs the named test alone in a new process of this test binary, where no metaschema is compiled yet, and returns its output.
func runFresh(t testing.TB, name string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), freshChildEnv+"="+name)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+name) {
		t.Fatalf("fresh process for %s: %v\n%s", name, err, out)
	}
	return string(out)
}

// readToolParameters is the read tool's parameter schema as PiG declares it (internal/codingagent/tools/read.go): no $schema, so the default draft.
const readToolParameters = `{"type":"object","required":["path"],"properties":{"path":{"type":"string","description":"Path to the file to read (relative or absolute)"},"offset":{"type":"number","description":"Line number to start reading from (1-indexed)"},"limit":{"type":"number","description":"Maximum number of lines to read"}}}`

// validateReadArguments compiles the read tool's parameters as ai/validate.go does and validates one call's arguments.
func validateReadArguments(args string) error {
	schema, err := UnmarshalJSON(strings.NewReader(readToolParameters))
	if err != nil {
		return err
	}
	c := NewCompiler()
	if err := c.AddResource("tool.json", schema); err != nil {
		return err
	}
	sch, err := c.Compile("tool.json")
	if err != nil {
		return err
	}
	value, err := UnmarshalJSON(strings.NewReader(args))
	if err != nil {
		return err
	}
	return sch.Validate(value)
}

func TestNothingCompiledAtStart(t *testing.T) {
	if metaschemasCompiledAtStart {
		t.Fatal("a metaschema was compiled before any schema was")
	}
}

// Validating the read tool's arguments in a new process compiles only the default draft's metaschema (2020-12) and loads only its 9 documents.
func TestReadToolValidationCompilesOnlyItsDraft(t *testing.T) {
	if !freshChild(t.Name()) {
		runFresh(t, t.Name())
		return
	}
	var loads atomic.Int32
	load := loadMeta
	loadMeta = func(url string) (any, error) {
		loads.Add(1)
		return load(url)
	}
	defer func() { loadMeta = load }()
	if err := validateReadArguments(`{"path":"main.go","offset":1,"limit":20}`); err != nil {
		t.Fatal(err)
	}
	for _, d := range []*Draft{Draft4, Draft6, Draft7, Draft2019} {
		if d.sch != nil {
			t.Errorf("draft %d metaschema compiled by a $schema-less tool schema", d.version)
		}
	}
	if Draft2020.sch == nil {
		t.Error("default draft 2020-12 metaschema not compiled")
	}
	if n := loads.Load(); n > 9 {
		t.Errorf("loadMeta ran %d times, want at most 9 (the 2020-12 metaschema and its 8 vocabularies)", n)
	}
}

// A draft's metaschema and vocabulary schemas compile once, on the draft's first use.
func TestMetaschemaCompiledOncePerDraft(t *testing.T) {
	for _, d := range allDrafts {
		compile := func() {
			c := NewCompiler()
			if err := c.AddResource("s.json", map[string]any{"$schema": d.url, "type": "string"}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Compile("s.json"); err != nil {
				t.Fatal(err)
			}
		}
		compile()
		if d.sch == nil {
			t.Fatalf("draft %d has no metaschema after its first use", d.version)
		}
		if len(d.vocabSchemas) != len(d.allVocabs) {
			t.Fatalf("draft %d compiled %d of %d vocabularies", d.version, len(d.vocabSchemas), len(d.allVocabs))
		}
		for name, vocab := range d.vocabSchemas {
			if vocab == nil {
				t.Fatalf("draft %d vocabulary %q not compiled", d.version, name)
			}
		}
		first := d.sch
		compile()
		if d.sch != first {
			t.Fatalf("draft %d metaschema compiled again", d.version)
		}
	}
}

// Parallel first uses of every draft, of the default draft and of custom metaschemas over 2019-09 and 2020-12 vocabularies, in a new process; run it under -race.
func TestConcurrentFirstUseOfDrafts(t *testing.T) {
	if !freshChild(t.Name()) {
		runFresh(t, t.Name())
		return
	}
	cases := lazyMetaCases()
	want := make([]string, len(cases))
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	const perCase = 4
	got := make([][perCase]string, len(cases))
	for i, c := range cases {
		for j := range perCase {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				got[i][j] = c.render()
			}()
		}
	}
	start.Done()
	done.Wait()
	for i, c := range cases {
		want[i] = c.render()
		for j := range perCase {
			if got[i][j] != want[i] {
				t.Errorf("%s: concurrent first use rendered\n%s\nwant\n%s", c.name, got[i][j], want[i])
			}
		}
	}
	for _, d := range allDrafts {
		if d.sch == nil {
			t.Errorf("draft %d metaschema not compiled", d.version)
		}
	}
}

var updateLazyMetaGolden = flag.Bool("update-lazy-meta", false, "rewrite testdata/lazy_meta_golden.txt")

const lazyMetaGolden = "testdata/lazy_meta_golden.txt"

// The errors and messages of every case equal the golden output recorded with every metaschema compiled at package initialisation (v6.0.2 at 2a05b4ab0c, with FORK.md difference 7: an items error after prefixItems names the element's own index), both in a new process, where each case is its draft's first use, and here, after other tests.
func TestValidationOutputMatchesEagerMetaschemas(t *testing.T) {
	got := renderLazyMetaCases()
	if *updateLazyMetaGolden && !freshChild(t.Name()) {
		if err := os.WriteFile(lazyMetaGolden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(lazyMetaGolden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("validation output differs from %s:\n%s", lazyMetaGolden, got)
	}
	if !freshChild(t.Name()) {
		runFresh(t, t.Name())
	}
}

type lazyMetaCase struct {
	name      string
	resources [][2]string // url, document
	schema    string
	instances []string
}

var lazyMetaDrafts = []struct{ name, url string }{
	{"default", ""},
	{"draft-04", "http://json-schema.org/draft-04/schema#"},
	{"draft-06", "http://json-schema.org/draft-06/schema#"},
	{"draft-07", "http://json-schema.org/draft-07/schema#"},
	{"2019-09", "https://json-schema.org/draft/2019-09/schema"},
	{"2020-12", "https://json-schema.org/draft/2020-12/schema"},
	{"latest", "https://json-schema.org/schema"},
}

// customMeta declares a metaschema over two of a draft's vocabularies, so its schemas validate against the vocabulary schemas, not the draft's metaschema.
func customMeta(draft, prefix string) string {
	return `{"$schema":"` + draft + `","$id":"http://example.com/meta","$vocabulary":{"` + prefix + `core":true,"` + prefix + `applicator":true},"allOf":[{"$ref":"` + strings.TrimSuffix(draft, "schema") + `meta/core"},{"$ref":"` + strings.TrimSuffix(draft, "schema") + `meta/applicator"}]}`
}

func lazyMetaCases() []lazyMetaCase {
	bodies := []struct {
		name      string
		schema    string
		instances []string
	}{
		{"read tool", readToolParameters[1 : len(readToolParameters)-1], []string{`{"path":"a.go"}`, `{"path":"a.go","offset":3,"limit":1.5}`, `{}`, `{"path":1}`, `{"path":"a","offset":"x"}`, `[]`}},
		{"bad type name", `"type":"strin"`, []string{`"x"`}},
		{"negative minLength", `"minLength":-1`, []string{`""`}},
		{"required not array", `"required":"path"`, []string{`{}`}},
		{"nested bad type", `"properties":{"a":{"type":5}}`, []string{`{}`}},
		{"bad pattern", `"pattern":"("`, []string{`"x"`}},
		{"empty enum", `"enum":[]`, []string{`1`}},
		{"boolean exclusiveMaximum", `"maximum":5,"exclusiveMaximum":true`, []string{`5`, `4`}},
		{"numeric exclusiveMaximum", `"exclusiveMaximum":5`, []string{`5`, `4`}},
		{"format email", `"type":"string","format":"email"`, []string{`"x"`, `"a@b.c"`}},
		{"definitions ref", `"$ref":"#/definitions/a","definitions":{"a":{"type":"integer"}}`, []string{`"x"`, `1`}},
		{"defs ref", `"$ref":"#/$defs/a","$defs":{"a":{"type":"integer","minimum":2}}`, []string{`1`, `"x"`, `3`}},
		{"unevaluatedProperties", `"type":"object","properties":{"a":{}},"unevaluatedProperties":false`, []string{`{"a":1,"b":2}`, `{"a":1}`}},
		{"prefixItems", `"prefixItems":[{"type":"integer"}],"items":false`, []string{`[1,2]`, `["x"]`, `[1]`}},
		{"items array", `"items":[{"type":"integer"}],"additionalItems":false`, []string{`[1,2]`, `["x"]`}},
		{"dependencies", `"dependencies":{"a":["b"]},"dependentRequired":{"c":["d"]}`, []string{`{"a":1}`, `{"c":1}`}},
		{"if then else", `"if":{"type":"integer"},"then":{"minimum":3},"else":{"type":"string"}`, []string{`1`, `true`, `4`}},
		{"const contains", `"const":[1],"contains":{"const":2}`, []string{`[1]`, `[2]`}},
		{"nested draft-07 resource", `"$defs":{"x":{"$id":"http://example.com/x","$schema":"http://json-schema.org/draft-07/schema#","type":"strin"}},"definitions":{"y":{"$id":"http://example.com/y","$schema":"http://json-schema.org/draft-04/schema#","minimum":1,"exclusiveMinimum":true}},"$ref":"http://example.com/y"`, []string{`1`, `2`}},
		{"boolean subschemas", `"properties":{"a":false,"b":true}`, []string{`{"a":1}`, `{"b":1}`}},
		{"schema as instance", `"$ref":"https://json-schema.org/draft/2020-12/schema"`, []string{`{"type":"strin"}`, `{"minLength":-1}`, `{"type":"string"}`}},
		{"draft-07 schema as instance", `"$ref":"http://json-schema.org/draft-07/schema#"`, []string{`{"type":"strin"}`, `{"properties":{"a":{"minimum":"x"}}}`}},
	}
	var cases []lazyMetaCase
	for _, d := range lazyMetaDrafts {
		for _, b := range bodies {
			schema := "{" + b.schema + "}"
			if d.url != "" {
				schema = `{"$schema":"` + d.url + `",` + b.schema + "}"
			}
			cases = append(cases, lazyMetaCase{name: d.name + " " + b.name, schema: schema, instances: b.instances})
		}
	}
	for _, d := range []struct{ name, url, prefix string }{
		{"2019-09", "https://json-schema.org/draft/2019-09/schema", "https://json-schema.org/draft/2019-09/vocab/"},
		{"2020-12", "https://json-schema.org/draft/2020-12/schema", "https://json-schema.org/draft/2020-12/vocab/"},
	} {
		meta := [][2]string{{"http://example.com/meta", customMeta(d.url, d.prefix)}}
		for _, b := range []struct {
			name, schema string
			instances    []string
		}{
			{"vocab-only metaschema ignores validation keywords", `{"$schema":"http://example.com/meta","minLength":-1,"type":"strin"}`, []string{`""`}},
			{"vocab-only metaschema checks applicators", `{"$schema":"http://example.com/meta","properties":{"a":{"type":"string"}},"allOf":{}}`, []string{`{}`}},
			{"vocab-only metaschema valid", `{"$schema":"http://example.com/meta","properties":{"a":{"not":{}}}}`, []string{`{"a":1}`, `{}`}},
		} {
			cases = append(cases, lazyMetaCase{name: d.name + " " + b.name, resources: meta, schema: b.schema, instances: b.instances})
		}
	}
	cases = append(cases, lazyMetaCase{
		name:      "unknown vocabulary",
		resources: [][2]string{{"http://example.com/meta", `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"http://example.com/meta","$vocabulary":{"http://example.com/vocab/x":true}}`}},
		schema:    `{"$schema":"http://example.com/meta"}`,
	})
	return cases
}

func renderLazyMetaCases() string {
	var sb strings.Builder
	for _, c := range lazyMetaCases() {
		sb.WriteString(c.render())
	}
	return sb.String()
}

func (c lazyMetaCase) render() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "== %s\n", c.name)
	compiler := NewCompiler()
	for _, r := range c.resources {
		doc, err := UnmarshalJSON(strings.NewReader(r[1]))
		if err == nil {
			err = compiler.AddResource(r[0], doc)
		}
		fmt.Fprintf(&sb, "resource %s: %s\n", r[0], describeLazyMetaError(err))
	}
	doc, err := UnmarshalJSON(strings.NewReader(c.schema))
	if err != nil {
		panic(fmt.Sprintf("%s: %v", c.name, err))
	}
	if err := compiler.AddResource("http://example.com/schema.json", doc); err != nil {
		fmt.Fprintf(&sb, "add: %s\n", describeLazyMetaError(err))
		return sb.String()
	}
	sch, err := compiler.Compile("http://example.com/schema.json")
	fmt.Fprintf(&sb, "compile: %s\n", describeLazyMetaError(err))
	if err != nil {
		return sb.String()
	}
	for _, instance := range c.instances {
		value, err := UnmarshalJSON(strings.NewReader(instance))
		if err != nil {
			panic(fmt.Sprintf("%s: %v", c.name, err))
		}
		fmt.Fprintf(&sb, "validate %s: %s\n", instance, describeLazyMetaError(sch.Validate(value)))
	}
	return sb.String()
}

// describeLazyMetaError renders an error with, for a validation error, its verbose tree and detailed output; sibling causes are sorted, since the validator collects some of them from maps.
func describeLazyMetaError(err error) string {
	if err == nil {
		return "ok"
	}
	var ve *ValidationError
	switch e := err.(type) {
	case *ValidationError:
		ve = e
	case *SchemaValidationError:
		ve, _ = e.Err.(*ValidationError)
	}
	if ve == nil {
		return err.Error()
	}
	sortCauses(ve)
	detailed, jerr := json.Marshal(ve.DetailedOutput())
	if jerr != nil {
		panic(jerr)
	}
	return fmt.Sprintf("%s\n%#v\n%s", err.Error(), ve, detailed)
}

func sortCauses(e *ValidationError) {
	for _, cause := range e.Causes {
		sortCauses(cause)
	}
	slices.SortStableFunc(e.Causes, func(a, b *ValidationError) int {
		return strings.Compare(fmt.Sprintf("%#v", a), fmt.Sprintf("%#v", b))
	})
}

// TestFirstToolValidationChild measures, in a new process, the first validation of the read tool's arguments: wall time, allocations and bytes.
func TestFirstToolValidationChild(t *testing.T) {
	if !freshChild(t.Name()) {
		t.Skip("run by BenchmarkFirstToolValidation in a new process")
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	err := validateReadArguments(`{"path":"main.go","offset":1,"limit":20}`)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("first-validation ns=%d allocs=%d bytes=%d\n", elapsed.Nanoseconds(), after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc)
}

// BenchmarkFirstToolValidation runs each iteration's first validation of the read tool's arguments in a new process; run it with -benchtime=1x -count=10.
func BenchmarkFirstToolValidation(b *testing.B) {
	const name = "TestFirstToolValidationChild"
	var ns, allocs, bytes float64
	for range b.N {
		out := runFresh(b, name)
		_, line, ok := strings.Cut(out, "first-validation ")
		if !ok {
			b.Fatalf("no measurement in\n%s", out)
		}
		line, _, _ = strings.Cut(line, "\n")
		for field := range strings.FieldsSeq(line) {
			key, value, _ := strings.Cut(field, "=")
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				b.Fatal(err)
			}
			switch key {
			case "ns":
				ns += n
			case "allocs":
				allocs += n
			case "bytes":
				bytes += n
			}
		}
	}
	b.ReportMetric(ns/float64(b.N), "first-ns/op")
	b.ReportMetric(allocs/float64(b.N), "first-allocs/op")
	b.ReportMetric(bytes/float64(b.N), "first-B/op")
}
