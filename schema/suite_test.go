package schema_test

// Ports packages/schema/test/suite.ts: loads the vendored JSON-Schema-Test-Suite, its remote documents and the official meta-schemas.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/schema"
)

// suiteFile is one test file: its path below tests/ and its groups.
type suiteFile struct {
	name   string
	draft  schema.Draft
	groups []suiteGroup
}

// suiteGroup is a schema with values and whether each is valid.
type suiteGroup struct {
	description string
	schema      schema.JsonSchema
	tests       []suiteTest
}

type suiteTest struct {
	description string
	data        any
	valid       bool
}

const fixtureRoot = "testdata"

// unquoteJS reads a string as JSON.parse does: the escape of a lone surrogate is that unit (WTF-8 in a Go string), not U+FFFD.
func unquoteJS(raw []byte, text *string) error { return jsstring.UnmarshalKeepingSurrogates(raw, text) }

// mustDecode parses JSON text as JSON.parse does, with objects in document order.
func mustDecode(t testing.TB, text string) any {
	t.Helper()
	value, err := chordjson.DecodeUnquoting([]byte(text), unquoteJS)
	if err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	return value
}

func readJSON(t testing.TB, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return mustDecode(t, string(data))
}

func jsonFiles(t testing.TB, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			out = append(out, jsonFiles(t, path)...)
		} else if strings.HasSuffix(entry.Name(), ".json") {
			out = append(out, path)
		}
	}
	slices.Sort(out)
	return out
}

// suiteFiles returns every test file of drafts 2020-12 and 7.
func suiteFiles(t testing.TB) []suiteFile {
	t.Helper()
	root := filepath.Join(fixtureRoot, "json-schema-test-suite")
	var files []suiteFile
	for _, d := range []struct {
		dir   string
		draft schema.Draft
	}{{"draft2020-12", schema.Draft202012}, {"draft7", schema.Draft7}} {
		for _, path := range jsonFiles(t, filepath.Join(root, "tests", d.dir)) {
			rel, err := filepath.Rel(filepath.Join(root, "tests"), path)
			if err != nil {
				t.Fatal(err)
			}
			file := suiteFile{name: strings.TrimSuffix(filepath.ToSlash(rel), ".json"), draft: d.draft}
			for _, g := range readJSON(t, path).([]any) {
				group := g.(*schema.Object)
				sg := suiteGroup{description: group.Value("description").(string), schema: group.Value("schema")}
				for _, tc := range group.Value("tests").([]any) {
					test := tc.(*schema.Object)
					sg.tests = append(sg.tests, suiteTest{
						description: test.Value("description").(string),
						data:        test.Value("data"),
						valid:       test.Value("valid").(bool),
					})
				}
				file.groups = append(file.groups, sg)
			}
			files = append(files, file)
		}
	}
	return files
}

// suiteSchemas returns the remote documents and meta-schemas the tests reference, by URI.
func suiteSchemas(t testing.TB) map[string]schema.JsonSchema {
	t.Helper()
	schemas := map[string]schema.JsonSchema{}
	root := filepath.Join(fixtureRoot, "json-schema-test-suite")
	remotes := filepath.Join(root, "remotes")
	for _, path := range jsonFiles(t, remotes) {
		rel, err := filepath.Rel(remotes, path)
		if err != nil {
			t.Fatal(err)
		}
		schemas["http://localhost:1234/"+filepath.ToSlash(rel)] = readJSON(t, path)
	}
	meta := filepath.Join(fixtureRoot, "meta-schemas")
	schemas["http://json-schema.org/draft-07/schema"] = readJSON(t, filepath.Join(meta, "draft-07-schema.json"))
	schemas["https://json-schema.org/draft/2020-12/schema"] = readJSON(t, filepath.Join(meta, "draft2020-12-schema.json"))
	for _, vocabulary := range []string{"core", "applicator", "unevaluated", "validation", "meta-data", "format-annotation", "content"} {
		schemas["https://json-schema.org/draft/2020-12/meta/"+vocabulary] = readJSON(t, filepath.Join(meta, "draft2020-12-meta-"+vocabulary+".json"))
	}
	return schemas
}

// checked adapts a Validator to the tests: a check or an explanation that returns an error fails the test.
type checked struct {
	*schema.Validator
	t testing.TB
}

func (c checked) Check(value any) bool {
	ok, err := c.Validator.Check(value)
	if err != nil {
		c.t.Errorf("Check: %v", err)
	}
	return ok
}

func (c checked) Errors(value any) []schema.ValidationError {
	errs, err := c.Validator.Errors(value)
	if err != nil {
		c.t.Errorf("Errors: %v", err)
	}
	return errs
}
