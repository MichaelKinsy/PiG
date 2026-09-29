package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

func harnessReport(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("HARNESS_TOOLS %s %s\n", name, data)
}

// TestHarnessPairedProbe drives the same environment-backed public factories
// as Pi's tools.test.ts, and emits deterministic values for the real-Pi pair.
func TestHarnessPairedProbe(t *testing.T) {
	env := newEnv(t)
	writeFile(t, env, "test.txt", lines(100, "Line %d"))
	read := execute(t, CreateReadTool(nil), env, "read", map[string]any{"path": "test.txt", "offset": 41, "limit": 20})
	harnessReport(t, "read", textOutput(read))
	write := execute(t, CreateWriteTool(), env, "write", map[string]any{"path": "nested/edit.txt", "content": "alpha\nbeta\ngamma\ndelta\n"})
	edit := execute(t, CreateEditTool(), env, "edit", edits("nested/edit.txt", "alpha\n", "ALPHA\n", "gamma\n", "GAMMA\n"))
	details := edit.Details.(*EditToolDetails)
	harnessReport(t, "edit", []any{textOutput(write), textOutput(edit), details.Diff, details.Patch, readFile(t, env, "nested/edit.txt")})
	bmp := tinyBMP()
	if err := env.WriteFile(t.Context(), "image.bmp", bmp); err != nil {
		t.Fatal(err)
	}
	var received []any
	image := CreateReadTool(&ReadToolOptions{AutoResizeImages: new(false), ImageProcessor: func(ctx context.Context, data []byte, mime string, resize bool) (ReadImageProcessorResult, error) {
		received = []any{mime, resize, len(data)}
		return ReadImageProcessorResult{OK: true, Data: "converted", MimeType: "image/png", Hints: []string{"[Image converted from image/bmp to image/png.]"}}, nil
	}})
	result := execute(t, image, env, "image", map[string]any{"path": "image.bmp"})
	attachment := result.Content[1].(ai.ImageContent)
	harnessReport(t, "image", []any{received, textOutput(result), attachment.Data, attachment.MimeType})
	late := &lateOutputEnv{NodeExecutionEnv: env}
	var updates []string
	result, err := CreateBashTool(nil).Execute(t.Context(), "late", map[string]any{"command": "late"}, func(update harness.AgentToolResult, _ harness.AgentHarnessToolUpdateOptions) {
		updates = append(updates, textOutput(update))
	}, ExecutionToolContext{Env: late}, testInvocation{})
	if err != nil {
		t.Fatal(err)
	}
	late.late()
	harnessReport(t, "late", []any{textOutput(result), updates})
	synctest.Test(t, func(t *testing.T) {
		var checkpoints []string
		_, err := CreateBashTool(nil).Execute(t.Context(), "checkpoints", map[string]any{"command": "controlled"}, func(update harness.AgentToolResult, options harness.AgentHarnessToolUpdateOptions) {
			if options.Checkpoint {
				checkpoints = append(checkpoints, textOutput(update))
			}
		}, ExecutionToolContext{Env: &checkpointOutputEnv{newEnv(t)}}, testInvocation{})
		if err != nil {
			t.Fatal(err)
		}
		harnessReport(t, "checkpoints", checkpoints)
	})
	harnessReport(t, "write-queue", testAbortedMutation(t, false))
	harnessReport(t, "edit-queue", testAbortedMutation(t, true))
}
