package schema_test

// Ports packages/schema/test/coerce-typebox.test.ts (Pi b556cfe5): TypeBox 1.3.27's Value.Convert tests (test/typebox/runtime/value/convert, MIT, (c) Haydn Paterson), run against the conversion pass of the coercion (pass 2 alone, as Pi's `new Coercer(...).convertOnly(value)`).
//
// testdata/coerce-typebox.json holds every `it` of the TypeScript file, in its describe and it names, with the JSON of each TypeBox schema, input and expectation. testdata/gen-coerce-typebox.mjs generates it by running the TypeScript file against TypeBox 1.3.27: `node gen-coerce-typebox.mjs <pi-export-dir> coerce-typebox.json b556cfe5`.
//
// JSON data cannot carry an `undefined` or a bigint (a Symbol has no Go value), so a case whose schema, input or expectation holds one is not run from the data (it lists it as "noGoForm" with the kind; TestPiMainValueConvertWithBigIntAndUndefined in pi_main_bigint_upstream_test.go runs those 18 with Go values, a *big.Int and schema.Undefined): 18 cases (undefined: 8, bigint: 10); Pi's own 17 `it.skip` cases (bigint literals, a Symbol, a bigint array) are listed as skipped there. Each of the 186 other cases runs.

import (
	"testing"

	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

func TestCoerceTypeBoxUpstream(t *testing.T) {
	data := readJSON(t, "testdata/coerce-typebox.json").(*schema.Object)
	if got := data.Value("piCommit"); got != "b556cfe5" {
		t.Fatalf("coerce-typebox.json is from Pi commit %v, want b556cfe5", got)
	}
	ran, noGoForm, skipped := 0, 0, 0
	undefinedCases, bigintCases := 0, 0
	for _, g := range data.Value("groups").([]any) {
		group := g.(*schema.Object)
		t.Run(group.Value("describe").(string), func(t *testing.T) {
			for _, c := range group.Value("cases").([]any) {
				tc := c.(*schema.Object)
				name := tc.Value("name").(string)
				if _, ok := tc.Value("skipped").(string); ok {
					skipped++
					continue
				}
				if kind, ok := tc.Value("noGoForm").(string); ok {
					noGoForm++
					switch kind {
					case "undefined":
						undefinedCases++
					case "bigint":
						bigintCases++
					default:
						t.Errorf("%s: unexpected kind with no Go form: %s", name, kind)
					}
					continue
				}
				ran++
				t.Run(name, func(t *testing.T) {
					for _, a := range tc.Value("assertions").([]any) {
						assertion := a.(*schema.Object)
						got, err := schema.ConvertOnly(assertion.Value("schema"), assertion.Value("input"))
						if err != nil {
							t.Fatalf("Convert: %v", err)
						}
						if want, ok := assertion.Get("expected"); ok {
							if !equalJSON(got, want) {
								t.Fatalf("Convert(%s, %s) = %s, want %s", marshalled(assertion.Value("schema")), marshalled(assertion.Value("input")), marshalled(got), marshalled(want))
							}
							continue
						}
						v, err := schema.Compile(assertion.Value("schema"), schema.CompileOptions{Formats: formats.Formats()})
						if err != nil {
							t.Fatalf("Compile: %v", err)
						}
						valid, err := v.Check(got)
						if err != nil {
							t.Fatalf("Check: %v", err)
						}
						if want := assertion.Value("check").(bool); valid != want {
							t.Fatalf("Check(%s, %s) = %v, want %v", marshalled(assertion.Value("schema")), marshalled(got), valid, want)
						}
					}
				})
			}
		})
	}
	if ran != 186 || noGoForm != 18 || skipped != 17 || undefinedCases != 8 || bigintCases != 10 {
		t.Fatalf("cases: ran %d, no Go form %d (undefined %d, bigint %d), skipped %d; want 186, 18 (8, 10), 17 (Pi's file has 221 `it` and `it.skip`)", ran, noGoForm, undefinedCases, bigintCases, skipped)
	}
}
