package ai_test

// Ports packages/ai/test/validation-probes.test.ts (Pi c23b9ec1): the reviewers' probes of the schema switch, TypeBox-built and plain JSON
// (MCP-shaped) tool schemas with arguments through validateToolArguments. testdata/validation-probes.json holds every `expect(outcome(...)).toBe(...)` of that file
// with the schema as the JSON text a tool declaration carries (TypeBox values serialize to their JSON Schema; Refine, Codec and the other
// non-JSON constructs leave no mark), the arguments as JSON text and Pi's expected outcome. testdata/validation_probes_corpus.mjs regenerates it from the Pi test file.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type validationProbe struct {
	Test     string `json:"test"`
	Name     string `json:"name"`
	Schema   string `json:"schema"`
	Args     string `json:"args"`
	Expected string `json:"expected"`
}

func loadValidationProbes(t testing.TB) []validationProbe {
	t.Helper()
	corpus, err := os.ReadFile("testdata/validation-probes.json")
	if err != nil {
		t.Fatal(err)
	}
	var probes []validationProbe
	if err := json.Unmarshal(corpus, &probes); err != nil {
		t.Fatal(err)
	}
	return probes
}

// probeOutcome is the test file's outcome(): "ok <coerced arguments>", "REJECT <first error line>" or "THROW <first line of the message>".
func probeOutcome(result piResult) string {
	if result.Err == nil {
		return "ok " + *result.Ok
	}
	message := *result.Err
	if strings.HasPrefix(message, "Validation failed") {
		lines := strings.Split(message, "\n")
		if len(lines) < 2 {
			return "REJECT undefined"
		}
		return "REJECT " + strings.TrimSpace(lines[1])
	}
	first, _, _ := strings.Cut(message, "\n")
	return "THROW " + first
}

// The probe "keeps the validator of a schema object, as before, also when the schema is changed later" records Pi's WeakMap keyed by schema object identity: a
// mutated schema object keeps its first validator. A Go tool schema is JSON text or a decoded map, validated by its content, so a changed schema is a new schema;
// TestValidateToolArgumentsValidatesTheSchemaItIsGiven carries that behavior.
const identityCacheProbe = "reviewer probes (Sol) > keeps the validator of a schema object, as before, also when the schema is changed later"

func TestValidationProbesMatchPi(t *testing.T) {
	probes := loadValidationProbes(t)
	if len(probes) != 168 {
		t.Fatalf("corpus has %d probes, Pi's file has 168", len(probes))
	}
	for _, probe := range probes {
		if probe.Test == identityCacheProbe && strings.Contains(probe.Schema, `"minimum":10`) {
			continue
		}
		t.Run(probe.Test, func(t *testing.T) {
			if got := probeOutcome(pigValidate(piProbe{probe.Name, probe.Schema, probe.Args})); got != probe.Expected {
				t.Errorf("outcome\n got  %s\n want %s", got, probe.Expected)
			}
		})
	}
}

// Pi's validateToolArguments, given the JSON a tool declaration carries for each case, answers what the Pi test file asserts for the TypeBox value (Refine, Codec and the other non-JSON constructs leave no mark in the JSON, and the test's expectations hold without them), except the identity-cache probe.
func TestPiAnswersItsRecordedProbeOutcomes(t *testing.T) {
	rows := loadValidationProbes(t)
	probes := make([]piProbe, len(rows))
	for i, row := range rows {
		probes[i] = piProbe{row.Name, row.Schema, row.Args}
	}
	results := piValidate(t, probes)
	differing := 0
	for i, row := range rows {
		if got := probeOutcome(results[i]); got != row.Expected {
			if row.Test == identityCacheProbe && strings.Contains(row.Schema, `"minimum":10`) {
				continue
			}
			differing++
			t.Errorf("%s\n Pi now %s\n Pi test %s", row.Test, got, row.Expected)
		}
	}
	if differing != 0 {
		t.Errorf("%d of %d probes differ", differing, len(rows))
	}
}

// The Go form of the identity-cache probe: a tool's schema is validated by its content, so a schema changed after its first validation is the changed schema. (Pi keeps the validator of the first, as its WeakMap
// is keyed by the schema object; the probe records that as "as before".) The answer for the changed schema is Pi's validation of the same JSON.
func TestValidateToolArgumentsValidatesTheSchemaItIsGiven(t *testing.T) {
	number := map[string]any{"type": "number", "minimum": float64(0)}
	tool := ai.ToolSchema{Name: "late", Parameters: map[string]any{"type": "object", "properties": map[string]any{"n": number}, "required": []any{"n"}}}
	call := ai.ToolCall{Name: "late", Arguments: ai.JsonObject{"n": "4"}}
	got, err := ai.ValidateToolArguments(tool, call)
	if err != nil || got["n"] != float64(4) {
		t.Fatalf("before the change: %v, %v", got, err)
	}
	number["minimum"] = float64(10)
	_, err = ai.ValidateToolArguments(tool, call)
	if err == nil {
		t.Fatal("the changed schema was not applied")
	}
	changed := piProbe{"late", `{"type":"object","properties":{"n":{"type":"number","minimum":10}},"required":["n"]}`, `{"n":"4"}`}
	if want := piValidate(t, []piProbe{changed})[0]; want.Err == nil || err.Error() != *want.Err {
		t.Fatalf("error = %v, want Pi's %s", err, want)
	}
}
