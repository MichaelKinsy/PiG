package schema_test

// Ports packages/schema/test/conformance.test.ts: the official JSON-Schema-Test-Suite, drafts 2020-12 and 7, every test including optional and format tests. A test listed in knownFailures must fail; when one starts passing, remove it from the list.

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

var knownFailures = map[string]bool{
	// Vocabularies switched off by a custom meta-schema are not supported.
	"draft2020-12/vocabulary > schema that uses custom metaschema with with no validation vocabulary > no validation: invalid number, but it still validates": true,
	"draft2020-12/optional/format-assertion > schema that uses custom metaschema with format-assertion: false > format-assertion: false: invalid string":      true,
	"draft2020-12/optional/format-assertion > schema that uses custom metaschema with format-assertion: true > format-assertion: true: invalid string":        true,
	// A $ref to a document of another draft is evaluated with the compile's draft.
	"draft2020-12/optional/cross-draft > refs to historic drafts are processed as historic drafts > first item not a string is valid": true,
	"draft7/optional/cross-draft > refs to future drafts are processed as future drafts > missing bar is invalid":                     true,
	// contentMediaType and contentEncoding are annotations.
	"draft7/optional/content > validation of string-encoded content based on media type > an invalid JSON document":             true,
	"draft7/optional/content > validation of binary string-encoding > an invalid base64 string (% is not a valid character)":    true,
	"draft7/optional/content > validation of binary-encoded media type documents > a validly-encoded invalid JSON document":     true,
	"draft7/optional/content > validation of binary-encoded media type documents > an invalid base64 string that is valid JSON": true,
}

// pi: packages/schema/src/annotations.ts
// pi: packages/schema/src/checks.ts
// pi: packages/schema/src/compiler.ts
// pi: packages/schema/src/resolver.ts

func TestConformanceSuite(t *testing.T) {
	schemas := suiteSchemas(t)
	files := suiteFiles(t)
	if len(files) == 0 {
		t.Fatal("no suite files")
	}
	allFormats := formats.Formats()
	for _, file := range files {
		// Format tests assert formats; everywhere else `format` is an annotation, as draft 2020-12 specifies.
		options := schema.CompileOptions{Draft: file.draft, Schemas: schemas}
		if strings.Contains(file.name, "/optional/format/") {
			options.Formats = allFormats
		}
		t.Run(file.name, func(t *testing.T) {
			for _, group := range file.groups {
				t.Run(group.description, func(t *testing.T) {
					for _, test := range group.tests {
						known := knownFailures[file.name+" > "+group.description+" > "+test.description]
						t.Run(test.description, func(t *testing.T) {
							// A listed test fails when compiling fails or the check disagrees with the suite.
							failed := false
							validator, err := schema.Compile(group.schema, options)
							if err != nil || (checked{validator, t}).Check(test.data) != test.valid {
								failed = true
							}
							if known && !failed {
								t.Fatal("a known failure passes: remove it from knownFailures")
							}
							if !known && failed {
								if err != nil {
									t.Fatalf("compile: %v", err)
								}
								t.Fatalf("Check = %v, want %v", !test.valid, test.valid)
							}
						})
						t.Run(test.description+" (errors agree with check)", func(t *testing.T) {
							validator, err := schema.Compile(group.schema, options)
							if err != nil {
								t.Fatalf("compile: %v", err)
							}
							errs := checked{validator, t}.Errors(test.data)
							if (len(errs) == 0) != (checked{validator, t}).Check(test.data) {
								t.Fatalf("errors %v disagree with check", errs)
							}
						})
					}
				})
			}
		})
	}
}
