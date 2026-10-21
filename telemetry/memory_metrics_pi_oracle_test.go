package telemetry_test

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/telemetry/src/memory.ts

// numberSpec is a JSON number or {"$num": "NaN" | "Infinity" | "-Infinity"}, which JSON cannot carry.
type numberSpec struct {
	Value float64
	Text  string
}

func (n numberSpec) MarshalJSON() ([]byte, error) {
	if n.Text != "" {
		return json.Marshal(map[string]string{"$num": n.Text})
	}
	return json.Marshal(n.Value)
}

func (n numberSpec) float() float64 {
	switch n.Text {
	case "NaN":
		return math.NaN()
	case "Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	return n.Value
}

type callSpec struct {
	Value numberSpec `json:"value"`
	Attrs attrList   `json:"attrs,omitempty"`
	Ctx   string     `json:"ctx,omitempty"`
	Idx   int        `json:"idx"`
}

// stepSpec creates one instrument with the given options (an absent member is undefined) and records its calls, optionally inside a span.
type stepSpec struct {
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	Description *string      `json:"description,omitempty"`
	Unit        *string      `json:"unit,omitempty"`
	Boundaries  []numberSpec `json:"boundaries,omitzero"`
	InSpan      bool         `json:"inSpan,omitempty"`
	Calls       []callSpec   `json:"calls"`
}

// jsonNumber is JSON.stringify of a number: a non-finite number is null.
func jsonNumber(value float64) any {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return value
}

// measurementJSON writes getMetrics() as JSON.stringify does, members in the order Pi's measurement creates them.
type measurementJSON struct {
	Kind        string                   `json:"kind"`
	Name        string                   `json:"name"`
	Description *string                  `json:"description,omitempty"`
	Unit        *string                  `json:"unit,omitempty"`
	Boundaries  *[]any                   `json:"boundaries,omitempty"`
	Value       any                      `json:"value"`
	Attributes  telemetry.SpanAttributes `json:"attributes"`
	Context     map[string]string        `json:"context"`
}

type metricsProgram struct {
	Spans int        `json:"spans"`
	Steps []stepSpec `json:"steps"`
}

// The measurements Pi's InMemoryTelemetryContext records for seeded random instrument programs (counters, up-down counters and histograms with absent, empty and unusual
// metadata, boundaries that are unsorted, repeated or non-finite, non-finite and negative values, attributes with undefined and array values, and exemplar contexts that are kept spans,
// the open span, the no-op context, the recorder root or omitted) against PiG's, as the JSON of getMetrics().
func TestInMemoryMetricsMatchPiOnRandomPrograms(t *testing.T) {
	rng := rand.New(rand.NewSource(1402))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	attributes := func() attrList {
		if rng.Intn(4) == 0 {
			return nil
		}
		var out attrList
		for range rng.Intn(6) {
			name := pick("a", "b", "10", "2", "é", "")
			switch rng.Intn(6) {
			case 0:
				out = append(out, [2]any{name, pick("x", "", "é😀")})
			case 1:
				out = append(out, [2]any{name, float64(rng.Intn(10)) + []float64{0, 0.5}[rng.Intn(2)]})
			case 2:
				out = append(out, [2]any{name, rng.Intn(2) == 0})
			case 3:
				out = append(out, [2]any{name, []any{pick("p", "q"), pick("r", "s")}})
			case 4:
				out = append(out, [2]any{name, []any{}})
			default:
				out = append(out, [2]any{name, map[string]any{"$undef": true}})
			}
		}
		return out
	}
	value := func() numberSpec {
		switch rng.Intn(14) {
		case 0:
			return numberSpec{Text: "NaN"}
		case 1:
			return numberSpec{Text: "Infinity"}
		case 2:
			return numberSpec{Text: "-Infinity"}
		case 3, 4:
			return numberSpec{Value: -float64(1 + rng.Intn(5))}
		case 5:
			return numberSpec{Value: 0}
		case 6:
			return numberSpec{Value: 0.25 * float64(1+rng.Intn(20))}
		}
		return numberSpec{Value: float64(rng.Intn(100))}
	}
	text := func() *string {
		switch rng.Intn(4) {
		case 0:
			return nil
		case 1:
			return new("")
		}
		return new(pick("bytes", "By", "ms", "é"))
	}
	boundaries := func() []numberSpec {
		switch rng.Intn(9) {
		case 0:
			return nil
		case 1:
			return []numberSpec{}
		case 2:
			return []numberSpec{{Value: 1}, {Value: 10}, {Value: 100}}
		case 3:
			return []numberSpec{{Value: 10}, {Value: 1}}
		case 4:
			return []numberSpec{{Value: 1}, {Value: 1}}
		case 5:
			return []numberSpec{{Text: "NaN"}}
		case 6:
			return []numberSpec{{Value: 1}, {Text: "Infinity"}}
		case 7:
			return []numberSpec{{Value: -5}, {Value: 0}, {Value: 0.5}}
		}
		return []numberSpec{{Value: 7}}
	}
	var programs []metricsProgram
	for range 1000 {
		program := metricsProgram{Spans: rng.Intn(3)}
		for range 1 + rng.Intn(5) {
			step := stepSpec{Kind: pick("counter", "upDown", "histogram", "histogram"), Name: pick("m", "n", "10", ""), Description: text(), Unit: text(), InSpan: rng.Intn(3) == 0, Calls: []callSpec{}}
			if step.Kind == "histogram" {
				step.Boundaries = boundaries()
			}
			for range rng.Intn(5) {
				call := callSpec{Value: value(), Attrs: attributes(), Idx: rng.Intn(3)}
				if step.Kind == "histogram" {
					call.Ctx = pick("omit", "undefined", "noop", "recorder", "kept", "kept", "self")
					if call.Ctx == "kept" && program.Spans == 0 {
						call.Ctx = "omit"
					}
					if call.Ctx == "self" && !step.InSpan {
						call.Ctx = "omit"
					}
				}
				step.Calls = append(step.Calls, call)
			}
			program.Steps = append(program.Steps, step)
		}
		programs = append(programs, program)
	}
	input, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/metrics_oracle.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []json.RawMessage
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(programs) {
		t.Fatalf("Pi answered %d programs, want %d", len(want), len(programs))
	}
	failures, measured, exemplars := 0, 0, 0
	for i, program := range programs {
		recorder := telemetry.NewInMemoryTelemetryContext()
		var kept []telemetry.TelemetrySpan
		for range program.Spans {
			_ = recorder.StartSpan(telemetry.SpanOptions{Name: "k"}, func(span telemetry.TelemetrySpan) error {
				kept = append(kept, span)
				return nil
			})
		}
		for _, step := range program.Steps {
			options := telemetry.InstrumentOptions{Description: step.Description, Unit: step.Unit}
			run := func(self telemetry.TelemetrySpan) {
				switch step.Kind {
				case "counter", "upDown":
					var counter telemetry.TelemetryCounter
					if step.Kind == "counter" {
						counter = recorder.Counter(step.Name, options)
					} else {
						counter = recorder.UpDownCounter(step.Name, options)
					}
					for _, call := range step.Calls {
						counter.Add(call.Value.float(), attributesOf(call.Attrs))
					}
				default:
					histogramOptions := telemetry.HistogramOptions{InstrumentOptions: options}
					if step.Boundaries != nil {
						histogramOptions.Boundaries = make([]float64, len(step.Boundaries))
						for j, boundary := range step.Boundaries {
							histogramOptions.Boundaries[j] = boundary.float()
						}
					}
					histogram := recorder.Histogram(step.Name, histogramOptions)
					for _, call := range step.Calls {
						var context telemetry.TelemetryContext
						switch call.Ctx {
						case "noop":
							context = telemetry.NoopTelemetryContext
						case "recorder":
							context = recorder
						case "kept":
							context = kept[call.Idx%len(kept)]
						case "self":
							context = self
						}
						histogram.Record(call.Value.float(), attributesOf(call.Attrs), context)
					}
				}
			}
			if step.InSpan {
				_ = recorder.StartSpan(telemetry.SpanOptions{Name: "step"}, func(span telemetry.TelemetrySpan) error {
					run(span)
					return nil
				})
			} else {
				run(nil)
			}
		}
		recorded := recorder.GetMetrics()
		measured += len(recorded)
		got := make([]measurementJSON, len(recorded))
		for j, measurement := range recorded {
			got[j] = measurementJSON{Kind: string(measurement.Kind), Name: measurement.Name, Description: measurement.Description, Unit: measurement.Unit, Value: jsonNumber(measurement.Value), Attributes: measurement.Attributes}
			if measurement.Boundaries != nil {
				boundaries := make([]any, len(measurement.Boundaries))
				for k, boundary := range measurement.Boundaries {
					boundaries[k] = jsonNumber(boundary)
				}
				got[j].Boundaries = &boundaries
			}
			if measurement.Context != nil {
				got[j].Context = measurement.Context
				exemplars++
			}
		}
		gotJSON, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON = scopePattern.ReplaceAll(gotJSON, []byte("SCOPE"))
		// The texts are compared as written, so member order, attribute order included, is part of the comparison.
		gotCanonical, wantCanonical := gotJSON, []byte(want[i])
		if !bytes.Equal(gotCanonical, wantCanonical) {
			if failures++; failures <= 3 {
				programJSON, _ := json.Marshal(program)
				t.Errorf("program %d %s:\n PiG %s\n Pi  %s", i, programJSON, gotCanonical, wantCanonical)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d programs differ from Pi", failures, len(programs))
	}
	if measured < len(programs) || exemplars < len(programs)/10 {
		t.Fatalf("the programs recorded %d measurements and %d exemplars in %d programs", measured, exemplars, len(programs))
	}
}
