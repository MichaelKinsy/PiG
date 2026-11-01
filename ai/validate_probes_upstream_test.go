package ai

// Ports packages/ai/test/validation-probes.test.ts (Pi b556cfe5): the reviewers' probes of the schema switch, TypeBox-built and plain JSON
// (MCP-shaped) tool schemas with arguments through validateToolArguments. testdata/validation-probes.json holds every `expect(outcome(...)).toBe(...)` of that file
// with the schema as the JSON text a tool declaration carries (TypeBox values serialize to their JSON Schema; Refine, Codec and the other
// non-JSON constructs leave no mark), the arguments as JSON text and Pi's expected outcome. testdata/validation_probes_corpus.mjs regenerates it from the Pi test file.

import (
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
)

//go:embed testdata/validation-probes.json
var validationProbeCorpus []byte

type validationProbe struct {
	Test     string `json:"test"`
	Name     string `json:"name"`
	Schema   string `json:"schema"`
	Args     string `json:"args"`
	Expected string `json:"expected"`
}

func loadValidationProbes(t testing.TB) []validationProbe {
	t.Helper()
	var probes []validationProbe
	if err := json.Unmarshal(validationProbeCorpus, &probes); err != nil {
		t.Fatal(err)
	}
	return probes
}

// probeOutcome is the test file's outcome(): "ok <coerced arguments>", "REJECT <first error line>" or "THROW <first line of the message>".
func probeOutcome(name, schema, args string) string {
	validated, err := ValidateToolArgumentsJSON(name, json.RawMessage(schema), json.RawMessage(args))
	if err == nil {
		return "ok " + string(validated)
	}
	message := err.Error()
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
			if got := probeOutcome(probe.Name, probe.Schema, probe.Args); got != probe.Expected {
				t.Errorf("outcome\n got  %s\n want %s", got, probe.Expected)
			}
		})
	}
}
