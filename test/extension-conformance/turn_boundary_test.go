package extensionconformance

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func mutateTurnBoundary(args ...any) (any, error) {
	event := args[0].(extension.TurnEndEvent)
	if event.MessageEntryID != "boundary-assistant" {
		return nil, nil
	}
	event.Entries = append(event.Entries, extension.SessionBoundaryDraft{Type: "custom", CustomType: "mutated"})
	return nil, errors.New("turn-boundary-failure")
}
func snapshotTurnBoundary(args ...any) (any, error) {
	event := args[0].(extension.TurnEndEvent)
	if event.MessageEntryID != "boundary-assistant" {
		return nil, nil
	}
	entries := []extension.SessionBoundaryDraft{{Type: "custom", CustomType: "turn-boundary", Data: event}}
	return extension.BoundaryResult{Entries: &entries, Continue: new(true)}, nil
}

// Pi runner.ts:928-980 retains the proposal after a throwing boundary handler, rebuilds its preview, and sends the complete turn metadata to the next handler.
// Pi: packages/coding-agent/src/core/extensions/types.ts:1030 (TurnEndEvent.turnIndex); packages/coding-agent/src/core/extensions/types.ts:974 (BoundaryContextPreview.contextMessages); packages/coding-agent/src/core/extensions/types.ts:976 (BoundaryContextPreview.pendingMessages).
func TestTurnBoundaryMetadataMutationAndPreviewAcrossSDKs(t *testing.T) {
	for _, test := range allHarnessCases() {
		t.Run(test.name, func(t *testing.T) {
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("turn boundary complete")
				}
			})
			base := extension.TurnEndEvent{Type: "turn_end", TurnIndex: 7, MessageEntryID: "boundary-assistant", ToolResultEntryIds: []string{"boundary-tool"}, Message: wireAgentMessage(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "failed"}}, "stopReason": "error"}), ToolResults: []extension.ToolResultMessage{{Role: "toolResult", ToolCallID: "call", ToolName: "noop", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result"}}}}, BoundaryState: &extension.BoundaryState{Outcome: extension.AgentActivityError}}
			build := func(drafts []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
				entries := make([]extension.ProjectedSessionEntry, len(drafts))
				for i, draft := range drafts {
					entries[i] = extension.ProjectedSessionEntry{SourceEntry: map[string]any{"id": "preview", "type": draft.Type, "customType": draft.CustomType}, Messages: []extension.AgentMessage{}}
				}
				return extension.BoundaryContextPreview{ContextEntries: entries, ContextMessages: []extension.AgentMessage{wireAgentMessage(map[string]any{"role": "custom", "customType": "context", "content": "preview context", "display": false, "timestamp": 0})}, LLMMessages: []ai.Message{ai.UserMessage{Content: ai.UserText("preview llm")}}, PendingMessages: []extension.AgentMessage{wireAgentMessage(map[string]any{"role": "custom", "customType": "pending", "content": "pending context", "display": false, "timestamp": 0})}, CanContinue: len(drafts) > 0}, nil
			}
			diagnostics := []string{}
			h.runner.AddErrorListener(func(err *extension.ExtensionError) { diagnostics = append(diagnostics, err.Error) })
			result, err := h.runner.EmitBoundary(t.Context(), base, build)
			if err != nil {
				t.Fatal(err)
			}
			if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "turn-boundary-failure") {
				t.Errorf("diagnostics=%v", diagnostics)
			}
			if !result.Valid || !result.Continue || len(result.Entries) != 1 || result.Entries[0].CustomType != "turn-boundary" {
				t.Fatalf("result=%+v", result)
			}
			drafts := []extension.SessionBoundaryDraft{{Type: "custom", CustomType: "mutated"}}
			preview, err := build(drafts)
			if err != nil {
				t.Fatal(err)
			}
			base.BoundaryState = &extension.BoundaryState{Entries: drafts, Continue: false, Outcome: extension.AgentActivityError, Context: preview}
			jsonValue := func(value any) any {
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var decoded any
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				return decoded
			}
			if got, want := jsonValue(result.Entries[0].Data), jsonValue(base); !reflect.DeepEqual(got, want) {
				t.Errorf("complete boundary event=%v, want %v", got, want)
			}
		})
	}
}

// wireAgentMessage decodes a wire-shaped message into the host AgentMessage.
func wireAgentMessage(fields map[string]any) extension.AgentMessage {
	raw, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	var message extension.AgentMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		panic(err)
	}
	return message
}
