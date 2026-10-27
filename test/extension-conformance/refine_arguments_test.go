package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The "probe" cases of test/parity/sdk-vectors/pi-ai/validateToolArguments.json: Pi's own validateToolArguments for a tool whose parameters carry TypeBox refinements, with the answer
// (the validated arguments, or the error the model sees) and the order of every predicate and message call TypeBox made on the way.
type refineVectorCase struct {
	Name   string
	Args   []json.RawMessage
	Result struct {
		Args   json.RawMessage `json:"args"`
		Error  *string         `json:"error"`
		Calls  []string        `json:"calls"`
		Schema string          `json:"schema"`
	}
}

func refineVectorCases(t testing.TB) []refineVectorCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "parity", "sdk-vectors", "pi-ai", "validateToolArguments.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Cases []refineVectorCase }
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	var probes []refineVectorCase
	for _, c := range file.Cases {
		if strings.HasPrefix(c.Name, "probe: ") {
			probes = append(probes, c)
		}
	}
	if len(probes) == 0 {
		t.Fatal("validateToolArguments.json has no probe cases")
	}
	return probes
}

// refineProbeParameters is the parameters JSON Pi's Type wrote for the probe tool. The in-process Go reference lists a tool of that name with it, so every transport lists the same tools
// (TestConformance_TransportsMatch).
func refineProbeParameters() json.RawMessage {
	raw, err := os.ReadFile(filepath.Join("..", "parity", "sdk-vectors", "pi-ai", "validateToolArguments.json"))
	if err != nil {
		panic(err)
	}
	var file struct {
		Cases []refineVectorCase
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		panic(err)
	}
	for _, c := range file.Cases {
		if strings.HasPrefix(c.Name, "probe: ") {
			return json.RawMessage(c.Result.Schema)
		}
	}
	panic("validateToolArguments.json has no probe cases")
}

// toolRefiner is a tool's refinements as the agent loop reaches them: the sites its definition lists and the call that asks its extension to run one.
type toolRefiner struct{ def extension.ToolDefinition }

func (r toolRefiner) RefinementSites() []ai.RefinementSite { return r.def.RefinementSites }
func (r toolRefiner) Refine(ctx context.Context, path string, index int, value any, withMessage bool) (ai.RefineOutcome, error) {
	return r.def.RefineArguments(ctx, path, index, value, withMessage)
}

// TestConformance_RefinedToolArgumentsAgreeAcrossSDKs: every SDK registers the tool "refine-probe" with parameters built by Type and Type.Refine (the Node runtime runs Pi's own), and the host
// validates a tool call's arguments with it. For each call of the vector the answer is Pi's (the validated arguments, or the exact message the model sees, a thrown predicate's message
// included) and so is the sequence of calls the extension receives (Pi's Check runs the predicates in its order, Errors again, anyOf takes the first branch that passes, a throw ends it).
func TestConformance_RefinedToolArgumentsAgreeAcrossSDKs(t *testing.T) {
	cases := refineVectorCases(t)
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var def *extension.ToolDefinition
			for _, tool := range h.runner.Tools() {
				if tool.Definition.Name == "refine-probe" {
					definition := tool.Definition
					def = &definition
				}
			}
			if def == nil {
				t.Fatal("refine-probe is not registered")
			}
			if len(def.RefinementSites) == 0 || def.RefineArguments == nil {
				t.Fatal("the host does not know the refinements of refine-probe")
			}
			readLog := func() []string {
				before := len(*h.notify)
				if !h.runner.ExecuteCommand(context.Background(), "refine_log", "") {
					t.Fatal("no refine_log command")
				}
				var reported string
				pollUntilConformance(t, "refine_log: no report", func() bool {
					for _, n := range (*h.notify)[before:] {
						if rest, ok := strings.CutPrefix(n, "refine_log:"); ok {
							reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
							return true
						}
					}
					return false
				})
				var calls []string
				if err := json.Unmarshal([]byte(reported), &calls); err != nil {
					t.Fatalf("log %q: %v", reported, err)
				}
				if calls == nil {
					calls = []string{}
				}
				return calls
			}
			schema := def.ValidationParameters
			if len(schema) == 0 {
				schema = def.Parameters
			}
			readLog() // clear what registration and earlier cells left
			for _, c := range cases {
				var toolCall struct {
					Arguments json.RawMessage `json:"arguments"`
				}
				if err := json.Unmarshal(c.Args[1], &toolCall); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				validated, err := ai.ValidateToolArgumentsRefined(ctx, "probe", schema, toolCall.Arguments, toolRefiner{*def})
				cancel()
				calls := readLog()
				wantCalls := c.Result.Calls
				if wantCalls == nil {
					wantCalls = []string{}
				}
				switch {
				case c.Result.Error != nil:
					if err == nil || err.Error() != *c.Result.Error {
						t.Errorf("%s: error %v\n want %q", c.Name, err, *c.Result.Error)
					}
				case err != nil:
					t.Errorf("%s: error %v, Pi accepts", c.Name, err)
				default:
					var got, want any
					_ = json.Unmarshal(validated, &got)
					_ = json.Unmarshal(c.Result.Args, &want)
					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s: arguments %s, Pi %s", c.Name, validated, c.Result.Args)
					}
				}
				if !reflect.DeepEqual(calls, wantCalls) {
					t.Errorf("%s: calls %v\n  Pi %v", c.Name, calls, wantCalls)
				}
			}
		})
	}
}
