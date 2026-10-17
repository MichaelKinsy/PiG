package ai

// pi: packages/ai/src/api/constrained-sampling.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type samplingStep struct {
	Next  string `json:"next"`
	Close bool   `json:"close"`
}

type samplingProbe struct {
	Op       string          `json:"op"`
	Tool     json.RawMessage `json:"tool,omitempty"`
	Supports bool            `json:"supports,omitempty"`
	Check    bool            `json:"check,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
	Property string          `json:"property,omitempty"`
	Steps    []samplingStep  `json:"steps,omitempty"`
}

type samplingResult struct {
	OK    json.RawMessage `json:"ok"`
	Undef bool            `json:"undef"`
	Err   *string         `json:"err"`
	Steps []samplingResult
}

type samplingGen struct{ random *rand.Rand }

func (g samplingGen) pick(options ...string) string { return options[g.random.Intn(len(options))] }

// schema draws strict-mode inputs: well-formed ones and each construct the converter must reject (boolean and tuple schemas, unions of structured schemas, reference and combinator keywords, schema-valued
// additionalProperties, malformed required/properties, unknown required names) with scalar, nullable and nested variants.
func (g samplingGen) schema(depth int) string {
	scalars := []string{`{"type":"string"}`, `{"type":"number"}`, `{"type":"integer"}`, `{"type":"boolean"}`, `{"type":"null"}`, `{"type":["string","null"]}`, `{"type":[1,"null"]}`, `{"type":["object",1]}`, `{"type":[1,"array"]}`, `{"type":["integer",{"x":1}]}`, `{"const":null}`, `{"enum":[1,null]}`, `{"enum":["a","b"]}`, `{}`, `true`, `false`,
		`{"type":"string","format":"uri"}`, `{"type":"string","pattern":"^a"}`, `{"type":"string","minLength":1}`, `{"$ref":"#/x"}`, `{"not":{"type":"string"}}`, `{"allOf":[{"type":"string"}]}`, `{"dependencies":{}}`, `{"anyOf":[]}`, `{"anyOf":"x"}`, `[]`, `"s"`, `null`, `7`}
	if depth <= 0 {
		return scalars[g.random.Intn(len(scalars))]
	}
	switch g.random.Intn(9) {
	case 0:
		return scalars[g.random.Intn(len(scalars))]
	case 1:
		return `{"anyOf":[` + g.schema(depth-1) + `,` + g.schema(depth-1) + `]}`
	case 2:
		return `{"type":"array","items":` + g.schema(depth-1) + `}`
	case 3:
		return `{"type":"array","items":[` + g.schema(depth-1) + `]}`
	case 4:
		return `{"properties":{"a":` + g.schema(depth-1) + `}}`
	default:
		names := []string{"a", "b", "c", "10", "2", "d"}
		count := g.random.Intn(4)
		var properties, required []string
		for i := range count {
			name := names[g.random.Intn(len(names))]
			properties = append(properties, fmt.Sprintf("%q:%s", name, g.schema(depth-1)))
			if g.random.Intn(2) == 0 {
				required = append(required, fmt.Sprintf("%q", name))
			}
			_ = i
		}
		out := `{"type":` + g.pick(`"object"`, `"object"`, `"object"`, `["object","null"]`, `"string"`) + `,"properties":` + g.pick(`{`+strings.Join(properties, ",")+`}`, `{`+strings.Join(properties, ",")+`}`, `{`+strings.Join(properties, ",")+`}`, `[]`, `"x"`, `null`)
		out += g.pick(``, `,"required":[`+strings.Join(required, ",")+`]`, `,"required":[`+strings.Join(required, ",")+`]`, `,"required":["zz"]`, `,"required":[1]`, `,"required":"a"`, `,"required":[]`)
		out += g.pick(``, ``, `,"additionalProperties":false`, `,"additionalProperties":true`, `,"additionalProperties":{"type":"string"}`, `,"description":"d"`)
		return out + `}`
	}
}

func (g samplingGen) grammarParameters() string {
	return g.pick(
		`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`,
		`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`,
		`{"type":"object","properties":{"input":{"type":"number"}},"required":["input"]}`,
		`{"type":"object","properties":{"input":{"type":"string"},"b":{"type":"string"}},"required":["input","b"]}`,
		`{"type":"object","properties":{"b":{"type":"string"}},"required":["input"]}`,
		`{"type":"object","properties":{"input":{}},"required":["input"]}`,
		`{"type":"object","properties":{"input":{"type":"string"}},"required":"input"}`,
		`{"type":"object","properties":{"input":{"type":"string"}},"required":[1]}`,
		`{"type":"object","properties":{"input":{"type":"string"}}}`,
		`{"type":"string"}`, `{"properties":{"input":{"type":"string"}},"required":["input"]}`, `{"type":"object","properties":null,"required":["input"]}`)
}

func (g samplingGen) tool(parameters string) string {
	config := g.pick(``, `,"constrainedSampling":{"type":"json_schema","strict":"require"}`, `,"constrainedSampling":{"type":"json_schema","strict":"prefer"}`, `,"constrainedSampling":{"type":"json_schema"}`,
		`,"constrainedSampling":{"type":"json_schema","strict":"require"}`, `,"constrainedSampling":{"type":"json_schema","strict":"prefer"}`,
		`,"constrainedSampling":{"type":"grammar","variants":{"openai_lark":"start: x"}}`, `,"constrainedSampling":{"type":"grammar","variants":{"openai_regex":"a+"}}`,
		`,"constrainedSampling":{"type":"grammar","variants":{"openai_lark":"\u00a0 ","openai_regex":"\ufeffx"}}`, `,"constrainedSampling":{"type":"grammar","variants":{"openai_lark":"\u0085","openai_regex":"\u200b"}}`,
		`,"constrainedSampling":{"type":"grammar","variants":{"openai_lark":" \t","openai_regex":""}}`, `,"constrainedSampling":{"type":"grammar","variants":{}}`)
	return `{"name":` + g.pick(`"t"`, `"we\"ird"`, `"ü"`) + `,"description":"d","parameters":` + parameters + config + `}`
}

func samplingProbes() []samplingProbe {
	g := samplingGen{rand.New(rand.NewSource(20260956))}
	var probes []samplingProbe
	for range 4000 {
		parameters := g.schema(3)
		if !strings.HasPrefix(parameters, `{`) || g.random.Intn(3) != 0 && !strings.HasPrefix(parameters, `{"type":"object"`) {
			parameters = `{"type":"object","properties":{"a":` + parameters + `,"b":{"type":"string"}},"required":["b"]}`
		}
		tool := json.RawMessage(g.tool(parameters))
		probes = append(probes, samplingProbe{Op: "strict", Tool: tool}, samplingProbe{Op: "resolveStrict", Tool: tool, Supports: g.random.Intn(4) != 0, Check: g.random.Intn(2) == 0})
		grammarTool := json.RawMessage(g.tool(g.grammarParameters()))
		probes = append(probes, samplingProbe{Op: "grammar", Tool: grammarTool, Supports: g.random.Intn(5) != 0})
	}
	inputs := []string{"", "a", "ab", "a\"b", "a\nb", "<>&", "\u2028x", "\u007f", "é", "😀", "😀x", "\\", "a\u0000b", "x y"}
	for range 1500 {
		var steps []samplingStep
		current := ""
		for range 1 + g.random.Intn(5) {
			next := current
			switch g.random.Intn(6) {
			case 0:
				next = inputs[g.random.Intn(len(inputs))]
			case 1:
				next = ""
			default:
				next += inputs[g.random.Intn(len(inputs))]
			}
			steps = append(steps, samplingStep{Next: next, Close: g.random.Intn(4) == 0})
			current = next
		}
		probes = append(probes, samplingProbe{Op: "delta", Property: g.pick("input", "we\"ird", "é"), Steps: steps})
	}
	for _, args := range []string{`{"input":"x"}`, `{"input":1}`, `{"input":null}`, `{}`, `{"input":["x"]}`, `{"input":""}`} {
		probes = append(probes, samplingProbe{Op: "input", Tool: json.RawMessage(`{"name":"t"}`), Args: json.RawMessage(args), Property: "input"},
			samplingProbe{Op: "input", Tool: json.RawMessage(`{"name":"we\"ird\n"}`), Args: json.RawMessage(args), Property: "in\"put"})
	}
	return probes
}

func settleSampling[T any](value T, err error, present bool) (json.RawMessage, bool, *string) {
	if err != nil {
		message := err.Error()
		return nil, false, &message
	}
	if !present {
		return nil, true, nil
	}
	encoded, _ := json.Marshal(value)
	return encoded, false, nil
}

func sameSampling(t *testing.T, label string, got json.RawMessage, gotUndef bool, gotErr *string, want samplingResult) bool {
	t.Helper()
	if (gotErr == nil) != (want.Err == nil) || gotErr != nil && *gotErr != *want.Err || gotUndef != want.Undef {
		return false
	}
	if gotErr != nil || gotUndef {
		return true
	}
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if err := json.Unmarshal(want.OK, &b); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return reflect.DeepEqual(a, b)
}

// Pi's constrained-sampling.ts runs unchanged in Node on the same tools: the strict conversion (value and rejection message), the strict/grammar resolution for each support and strictness combination, and the streamed
// grammar-input deltas.
func TestConstrainedSamplingMatchesPi(t *testing.T) {
	probes := samplingProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "api", "constrained-sampling.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/constrained_sampling.mjs", src)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []samplingResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	check := func(key string, value any) bool { return key == "format" || key == "pattern" }
	differing, outcomes, lastGot := 0, map[string]int{}, ""
	for i, probe := range probes {
		var tool ToolSchema
		if probe.Tool != nil {
			if err := json.Unmarshal(probe.Tool, &tool); err != nil {
				t.Fatalf("probe %s: %v", probe.Tool, err)
			}
		}
		want := expected[i]
		var ok bool
		var label string
		switch probe.Op {
		case "strict":
			strict := true
			toolParameters, err := getJSONSchemaToolParameters(tool, &strict)
			parameters, order := toolParameters.Schema, toolParameters.order
			encoded, undef, message := settleSampling(parameters, err, true)
			ok, label = sameSampling(t, "strict", encoded, undef, message, want), "strict"
			// A request body writes the strict schema's members in the order makeJsonSchemaNodeStrict creates them.
			if ok && message == nil {
				ordered, err := marshalToolParameters(parameters, order)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(ordered, want.OK) {
					ok, encoded = false, ordered
				}
			}
			outcomes[fmt.Sprintf("%s-%v", probe.Op, message == nil)]++
			lastGot = string(encoded)
			if message != nil {
				lastGot = *message
			}
		case "resolveStrict":
			var keyword UnsupportedStrictSchemaKeywordCheck
			if probe.Check {
				keyword = check
			}
			result, err := resolveJSONSchemaStrictSampling(tool, probe.Supports, keyword)
			encoded, undef, message := settleSampling(result != nil && *result, err, result != nil)
			ok, label = sameSampling(t, "resolveStrict", encoded, undef, message, want), "resolveStrict"
			outcomes[fmt.Sprintf("%s-%v-%v", probe.Op, result != nil, message == nil)]++
		case "grammar":
			result, err := resolveGrammarConstrainedSampling(tool, probe.Supports)
			var value any
			if result != nil {
				value = map[string]string{"format": result.Format, "definition": result.Definition, "inputProperty": result.InputProperty}
			}
			encoded, undef, message := settleSampling(value, err, result != nil)
			ok, label = sameSampling(t, "grammar", encoded, undef, message, want), "grammar"
			outcomes[fmt.Sprintf("%s-%v-%v", probe.Op, result != nil, message == nil)]++
		case "input":
			var arguments map[string]any
			if err := json.Unmarshal(probe.Args, &arguments); err != nil {
				t.Fatal(err)
			}
			text, err := getGrammarToolInput(tool.Name, arguments, probe.Property)
			encoded, undef, message := settleSampling(text, err, true)
			ok, label = sameSampling(t, "input", encoded, undef, message, want), "input"
		default:
			buffer := &grammarToolInputJSONBuffer{}
			ok, label = len(want.Steps) == len(probe.Steps), "delta"
			for j, step := range probe.Steps {
				delta, present, err := appendGrammarToolInputJSONDelta(buffer, probe.Property, step.Next, step.Close)
				encoded, undef, message := settleSampling(delta, err, present)
				if j < len(want.Steps) && !sameSampling(t, "delta", encoded, undef, message, want.Steps[j]) {
					ok = false
				}
				if message == nil && !undef {
					outcomes["delta-emitted"]++
				}
				if message != nil {
					outcomes["delta-error"]++
				}
			}
		}
		if !ok {
			if differing++; differing <= 10 {
				wantErr := "-"
				if want.Err != nil {
					wantErr = *want.Err
				}
				t.Errorf("%s\n  tool %s\n  Pi ok=%s undef=%v err=%s steps=%d\n  Pig %s", label, probe.Tool, want.OK, want.Undef, wantErr, len(want.Steps), lastGot)
			}
		}
	}
	if differing > 10 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	for _, key := range []string{"strict-true", "strict-false", "resolveStrict-true-true", "resolveStrict-false-true", "resolveStrict-false-false", "grammar-true-true", "grammar-false-true", "grammar-false-false", "delta-emitted", "delta-error"} {
		if outcomes[key] < 10 {
			t.Errorf("probes are one-sided: %s = %d", key, outcomes[key])
		}
	}
}
