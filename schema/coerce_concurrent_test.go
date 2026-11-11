package schema_test

// A CoercingValidator is safe for concurrent use, like a Validator: the state of a Coerce call is its own, and what the calls share (the compiler of the checks a union conversion asks for, the parsed keywords of every schema node) is built once. Run with -race.

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/schema"
)

func TestCoercingValidatorIsSafeForConcurrentUse(t *testing.T) {
	schemas := map[string]string{
		"tool schema": `{
			"type": "object",
			"properties": {
				"path": { "type": "string" },
				"offset": { "type": "integer", "minimum": 1 },
				"tags": { "type": "array", "items": { "type": "string" } },
				"mode": { "anyOf": [{ "type": "number" }, { "type": "null" }] }
			},
			"required": ["path"]
		}`,
		"recursive union": `{
			"type": "object",
			"properties": { "t": { "$ref": "#/$defs/Tree" } },
			"$defs": { "Tree": { "anyOf": [{ "type": "string" }, { "type": "array", "items": { "$ref": "#/$defs/Tree" } }] } }
		}`,
		"patterns and additional properties": `{
			"type": "object",
			"patternProperties": { "^n_": { "type": ["integer", "null"] } },
			"additionalProperties": { "type": "string" }
		}`,
		"bookended $dynamicRef": `{
			"$id": "https://example.com/tree.json",
			"$dynamicAnchor": "node",
			"type": "object",
			"properties": { "n": { "type": "integer" }, "children": { "type": "array", "items": { "$dynamicRef": "#node" } } }
		}`,
	}
	inputs := map[string][]string{
		"tool schema":                        {`{"path":"a","offset":"3","tags":"x","mode":"4"}`, `{"path":5,"offset":null}`, `{"path":"a"}`, `{"offset":"x"}`},
		"recursive union":                    {`{"t":[5,[6,["7"]]]}`, `{"t":{"a":1}}`, `{"t":"s"}`, `{"t":[[[[1]]]]}`},
		"patterns and additional properties": {`{"n_a":"5","other":7}`, `{"n_b":null,"x":true}`, `{}`, `{"n_c":"z"}`},
		"bookended $dynamicRef":              {`{"n":"4","children":[{"n":"5"}]}`, `{"n":"x"}`, `{"children":[{"children":[]}]}`},
	}
	for name, text := range schemas {
		t.Run(name, func(t *testing.T) {
			validator := coercing(t, mustDecode(t, text), schema.CompileOptions{})
			type outcome struct {
				coerced string
				ok      bool
			}
			expected := make([]outcome, len(inputs[name]))
			for i, input := range inputs[name] {
				coerced := coerceOf(t, validator, mustDecode(t, input))
				expected[i] = outcome{marshalled(coerced), parseOf(t, validator, mustDecode(t, input)).OK}
			}
			// A fresh validator, so the concurrent calls also race to build the coercer and each node's keywords.
			fresh := coercing(t, mustDecode(t, text), schema.CompileOptions{})
			var wg sync.WaitGroup
			for g := range 16 {
				wg.Go(func() {
					for round := range 50 {
						i := (g + round) % len(inputs[name])
						input := mustDecode(t, inputs[name][i])
						coerced, err := fresh.Coerce(input)
						if err != nil {
							t.Errorf("Coerce: %v", err)
							return
						}
						result, err := fresh.Parse(input)
						if err != nil {
							t.Errorf("Parse: %v", err)
							return
						}
						if got := (outcome{marshalled(coerced), result.OK}); got != expected[i] {
							t.Errorf("input %s: concurrent result %+v, sequential %+v", inputs[name][i], got, expected[i])
							return
						}
					}
				})
			}
			wg.Wait()
		})
	}
}
