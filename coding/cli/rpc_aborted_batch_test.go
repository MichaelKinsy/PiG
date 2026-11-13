package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

// Pi 4438c58ba (#9986) through a real Session in RPC mode: the faux provider asks for two tools at once (read, bash) and an
// extension's tool_call handler aborts while it decides about the first. A client sees, in this order: both calls started and
// ended "Operation aborted" (the second without its handler being asked, so nothing ran), a tool result message for each call
// in call order, the turn's end with both results, and then the aborted response of the next request.
func TestRPCAbortedBatchAnswersEveryCallInOrder(t *testing.T) {
	t.Parallel()
	fixture, err := filepath.Abs(filepath.Join("testdata", "abort-first-tool-call.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1"}, "--model", "test-faux/faux-1", "--no-session", "-e", fixture)
	p.send(`{"id":"prompt","type":"prompt","message":"Run: parallel reads"}`)
	text := func(holder any) string {
		object, _ := holder.(map[string]any)
		content, _ := object["content"].([]any)
		if len(content) != 1 {
			return fmt.Sprintf("<%d blocks>", len(content))
		}
		block, _ := content[0].(map[string]any)
		value, _ := block["text"].(string)
		return value
	}
	var order []string
	p.await("the aborted batch settling", func(record rpcRecord) bool {
		switch record["type"] {
		case "tool_execution_start":
			order = append(order, fmt.Sprintf("start %v", record["toolName"]))
		case "tool_execution_end":
			order = append(order, fmt.Sprintf("end %v %q isError=%v", record["toolName"], text(record["result"]), record["isError"]))
		case "message_end":
			message, _ := record["message"].(map[string]any)
			switch message["role"] {
			case "toolResult":
				order = append(order, fmt.Sprintf("result %v %q isError=%v", message["toolName"], text(message), message["isError"]))
			case "assistant":
				order = append(order, fmt.Sprintf("assistant %v", message["stopReason"]))
			}
		case "turn_end":
			results, _ := record["toolResults"].([]any)
			order = append(order, fmt.Sprintf("turn_end %d results", len(results)))
		case "agent_end":
			order = append(order, "agent_end")
		}
		return record["type"] == "agent_settled"
	})
	want := []string{
		"assistant toolUse",
		"start read", `end read "Operation aborted" isError=true`,
		"start bash", `end bash "Operation aborted" isError=true`,
		`result read "Operation aborted" isError=true`, `result bash "Operation aborted" isError=true`,
		"turn_end 2 results",
		"assistant aborted", "turn_end 0 results", "agent_end",
	}
	if !slices.Equal(order, want) {
		t.Fatalf("RPC events:\n  %q\nwant:\n  %q", order, want)
	}
	p.closeAndWait("after the aborted batch")
}
