package harness_test

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// Pi 1.2.0 test/harness-structured-output.test.ts:370 runs the real bash tool, so it lives in the external test package
// (durable/tools imports durable/harness).

func structuredJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// harness-structured-output.test.ts:370 it("gives callers of bash its retained output and exit code, also for a nonzero exit")
func TestCallersOfBashGetItsRetainedOutputAndExitCodeAlsoForANonzeroExit(t *testing.T) {
	setup := newCodingSetup()
	if err := setup.registry.Install(&durable.Extension{Name: "bash", Tools: []*durable.ToolRegistration{tools.CreateBashTool(nil)}}); err != nil {
		t.Fatal(err)
	}
	var received []durable.NestedToolExecutionResult
	probe := &durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "probe", Description: "probe", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}, Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		for _, command := range []string{"printf ok", "printf bad; exit 4"} {
			result, err := api.ExecuteTool(ctx, "bash", map[string]any{"command": command})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			received = append(received, result)
		}
		return durable.ToolExecutionResult{}, nil
	}}
	if err := setup.registry.Install(&durable.Extension{Name: "probe", Tools: []*durable.ToolRegistration{probe}}); err != nil {
		t.Fatal(err)
	}
	setup.faux.SetResponses([]ai.FauxResponseStep{toolCalls("probe", map[string]any{}, "c1"), answer("done")})
	opened, root := setup.open(t, storage.NewMemoryStorage(), envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: t.TempDir()}))
	if _, err := submit(t, root, "go").Wait(background); err != nil {
		t.Fatal(err)
	}
	closeHarness(t, opened)
	if len(received) != 2 {
		t.Fatalf("%d results, want 2", len(received))
	}
	if got := structuredJSON(t, received[0].StructuredOutput); received[0].IsError || !equalJSONText(t, got, `{"output":"ok","truncated":false,"exitCode":0}`) {
		t.Errorf("first %+v (%s), want ok with exit code 0", received[0], got)
	}
	if got := structuredJSON(t, received[1].StructuredOutput); !received[1].IsError || !equalJSONText(t, got, `{"output":"bad","truncated":false,"exitCode":4}`) || !slices.Equal(codesOf(received[1]), []string{"exit_code"}) {
		t.Errorf("second %+v (%s), want bad with exit code 4, an error and an exit_code diagnostic", received[1], got)
	}
}

// sameJSON compares two JSON texts by value: member order is not what these cases are about.
func equalJSONText(t *testing.T, got, want string) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal([]byte(got), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(a, b)
}

func codesOf(result durable.NestedToolExecutionResult) []string {
	codes := []string{}
	for _, diagnostic := range result.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}
