package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestSessionCustomToolsDefaultToParallel(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/src/agent-loop.ts:514 serializes only explicit "sequential" tools.
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan string, 2)
		release := make(chan struct{})
		tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "cooperate", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`), Execute: func(ctx context.Context, _ string, raw json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			if extension.FromContext(ctx) == nil {
				t.Error("extension tool has no runner context")
			}
			var args struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return extension.AgentToolResult{}, err
			}
			entered <- args.Value
			<-release
			return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: args.Value}}}, nil
		}}})
		if err != nil {
			t.Fatal(err)
		}
		h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{tool}}, admissionResponse("cooperate", "value", "first", "second"), fauxReply("done", ai.StopReasonStop, 0))
		done := make(chan []agent.AgentMessage, 1)
		go func() {
			messages, err := h.session.Send(t.Context(), "run both")
			if err != nil {
				t.Error(err)
			}
			done <- messages
		}()
		starts := []string{<-entered, <-entered}
		close(release)
		messages := <-done
		slices.Sort(starts)
		if !slices.Equal(starts, []string{"first", "second"}) {
			t.Fatal(starts)
		}
		results := toolResultMessages(messages)
		var texts []string
		for _, result := range results {
			if result.IsError {
				t.Fatal(result.Text())
			}
			texts = append(texts, result.Text())
		}
		if !slices.Equal(texts, []string{"first", "second"}) {
			t.Fatal(texts)
		}
		data, err := json.Marshal(texts)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("PARALLEL_TOOLS %s\n", data)
	})
}
