//go:build pigfault

package driver_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// tool.ts:84-90: before executing, the tool task records the intent `{phase: "execute", arguments: final, replay: tool.replay ?? "unsafe"}`,
// where final is the model's arguments run through tool.prepareArguments. The core reads the tool's replay policy and prepareArguments
// from the agent snapshot the driver sends (session.go agentJSON), so a tool with no replay field must record "unsafe", and a prepareArguments
// result of any Go type must reach the intent and execute as a JSON object.
// mutation-checked: session.go replay default (L605), PrepareArguments registration (L609) and result typing (L560).
func TestCoreToolRecordsItsPreparedArgumentsAndDefaultReplayBeforeExecuting(t *testing.T) {
	setup := chatSetup()
	reached := make(chan struct{})
	release := make(chan struct{})
	executed := make(chan any, 1)
	tool := conversationTool("count")
	tool.PrepareArguments = func(args any) (any, error) {
		return map[string]int{"n": 5}, nil
	}
	tool.Execute = func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		close(reached)
		executed <- args
		<-release
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
	}
	if err := setup.registry.Install(&durable.Extension{Name: "count", Tools: []*durable.ToolRegistration{tool}}); err != nil {
		t.Fatal(err)
	}
	setup.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("count", map[string]any{"n": "five"}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		fauxAnswer("done"),
	})
	h, root, _ := chatOpen(t, setup)
	submission := submitInput(t, root, "go")
	awaitChan(t, "the tool", reached)
	var intent string
	for id := durable.TaskId(1); id < 40 && intent == ""; id++ {
		task, err := h.GetTask(t.Context(), id)
		if err != nil || task == nil || task.Kind != "pi.tool" || task.State.Checkpoint == nil {
			continue
		}
		raw, _ := json.Marshal(task.State.Checkpoint)
		intent = string(raw)
	}
	close(release)
	if got := (<-executed); got == nil {
		t.Fatal("the tool executed with nil arguments")
	}
	if _, err := submission.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	var checkpoint struct {
		Phase     string         `json:"phase"`
		Arguments map[string]any `json:"arguments"`
		Replay    string         `json:"replay"`
	}
	if err := json.Unmarshal([]byte(intent), &checkpoint); err != nil || checkpoint.Phase != "execute" {
		t.Fatalf("checkpoint = %q (%v), want the execute intent", intent, err)
	}
	if checkpoint.Replay != "unsafe" {
		t.Errorf("replay = %q, want unsafe for a tool with no replay policy", checkpoint.Replay)
	}
	if n, _ := checkpoint.Arguments["n"].(float64); n != 5 || len(checkpoint.Arguments) != 1 {
		t.Errorf("arguments = %v, want the prepared {n: 5}", checkpoint.Arguments)
	}
}

// tool.ts:138-154: prepareArguments's result goes to validation whatever it is (`as JsonObject`); a result that is not an object fails validation and
// the call settles as an invalid_arguments error whose text shows the value the repair returned (validation.ts:347 `Received arguments`), the tool
// does not execute, and the run goes on. Pi 1.1.0 validateToolArguments with arguments "x", 5, [1] and null gives the texts below (node probe).
// mutation-checked: removing the nil guard in turnhost.go checkArguments panicked the core; narrowing the prepared value to an object
// (turnhost.go objOf, session.go a map result) shows null for "x", 5 and [1].
func TestCoreToolRefusesANonObjectPreparedResultAsInvalidArguments(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prepared any
		received string
	}{
		{"nil", nil, "null"},
		{"string", "x", `"x"`},
		{"number", 5, "5"},
		{"array", []int{1}, "[\n  1\n]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := chatSetup()
			executed := make(chan struct{}, 1)
			tool := conversationTool("count")
			tool.PrepareArguments = func(any) (any, error) { return tc.prepared, nil }
			tool.Execute = func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				executed <- struct{}{}
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
			}
			if err := setup.registry.Install(&durable.Extension{Name: "count", Tools: []*durable.ToolRegistration{tool}}); err != nil {
				t.Fatal(err)
			}
			setup.faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("count", map[string]any{"n": "five"}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
				fauxAnswer("done"),
			})
			_, root, _ := chatOpen(t, setup)
			settled, err := submitInput(t, root, "go").Wait(t.Context())
			if err != nil || settled.Status != durable.SubmissionDone {
				t.Fatalf("settled = %+v, %v", settled, err)
			}
			select {
			case <-executed:
				t.Fatal("the tool executed with arguments that failed validation")
			default:
			}
			want := "<harness>\n[error] Validation failed for tool \"count\":\n  - root: must be object\n\nReceived arguments:\n" + tc.received + "\n</harness>"
			var texts []string
			for _, entry := range chatEntries(t, root) {
				if entry.Kind != durable.ToolResultEntry.Kind {
					continue
				}
				for _, message := range entry.Model {
					if result, ok := message.(ai.ToolResultMessage); ok && result.IsError {
						for _, block := range result.Content {
							if text, ok := block.(ai.TextContent); ok {
								texts = append(texts, text.Text)
							}
						}
					}
				}
			}
			if len(texts) != 1 || texts[0] != want {
				t.Fatalf("error results = %q, want [%q]", texts, want)
			}
		})
	}
}
