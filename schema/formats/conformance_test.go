package formats_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

// pi: packages/schema/src/formats/typebox/regex.ts

// notFormatsTable are the format names of the official suite's optional/format files that Pi's `formats` table does not define. Pi compiles those files with the table and an unknown name is an annotation, so the suite's expectations for them are not format checks. Any other name missing from Formats() fails the test.
var notFormatsTable = map[string]bool{"unknown": true}

type suiteGroup struct {
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Tests       []struct {
		Description string          `json:"description"`
		Data        json.RawMessage `json:"data"`
		Valid       bool            `json:"valid"`
	} `json:"tests"`
}

// TestConformance replays the string-valued tests of the official JSON-Schema-Test-Suite optional/format files (drafts 2020-12 and 7) against Formats(). Non-string data is not a format check.
func TestConformance(t *testing.T) {
	table := formats.Formats()
	asserted := 0
	skipped := map[string]bool{}
	for _, draft := range []string{"draft2020-12", "draft7"} {
		files, err := filepath.Glob(filepath.Join("..", "testdata", "json-schema-test-suite", "tests", draft, "optional", "format", "*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no format fixtures for %s: %v", draft, err)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var groups []suiteGroup
			if err := json.Unmarshal(data, &groups); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			for _, group := range groups {
				var schema map[string]json.RawMessage
				if err := json.Unmarshal(group.Schema, &schema); err != nil {
					t.Fatalf("%s: %s: schema is not an object", file, group.Description)
				}
				for key := range schema {
					if key != "format" && key != "$schema" {
						t.Fatalf("%s: %s: schema has unexpected keyword %q", file, group.Description, key)
					}
				}
				var name string
				if err := json.Unmarshal(schema["format"], &name); err != nil {
					t.Fatalf("%s: %s: schema has no format name", file, group.Description)
				}
				if notFormatsTable[name] {
					skipped[name] = true
					continue
				}
				check, ok := table[name]
				if !ok {
					t.Errorf("%s: %s: format %q is not in Formats()", file, group.Description, name)
					continue
				}
				for _, test := range group.Tests {
					if len(test.Data) == 0 || test.Data[0] != '"' {
						continue
					}
					var value string
					if err := jsstring.UnmarshalKeepingSurrogates(test.Data, &value); err != nil {
						t.Fatalf("%s: %s: %v", file, test.Description, err)
					}
					asserted++
					if got := check(value); got != test.Valid {
						t.Errorf("%s: %s: %s: %s(%q) = %v, want %v", file, group.Description, test.Description, name, value, got, test.Valid)
					}
				}
			}
		}
	}
	if asserted < 500 {
		t.Errorf("asserted only %d string tests", asserted)
	}
	if len(skipped) != len(notFormatsTable) {
		t.Errorf("skipped names %v, want exactly %v", skipped, notFormatsTable)
	}
}
