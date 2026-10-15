package inproc

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// runner.ts:1437-1471: every handler's event carries the one shared options object (`systemPromptOptions: currentOptions`), so a later handler sees the forced prompt an
// earlier handler returned (`currentOptions.forceSystemPrompt = result.systemPrompt`, :1454-1456), and the combined result returns that object.
func TestBeforeAgentStartLaterHandlerSeesTheForcedPromptInTheSharedOptions(t *testing.T) {
	var seen *string
	runner := NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {
		func(args ...any) (any, error) {
			return &extension.BeforeAgentStartEventResult{SystemPrompt: new("forced by the first handler")}, nil
		},
		func(args ...any) (any, error) {
			seen = args[0].(extension.BeforeAgentStartEvent).SystemPromptOptions.ForceSystemPrompt
			return nil, nil
		},
	}}}}, t.TempDir())
	result, err := runner.EmitBeforeAgentStart(t.Context(), extension.UserPromptTrigger("p", nil), extension.BuildSystemPromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if seen == nil || *seen != "forced by the first handler" {
		t.Fatalf("the second handler's systemPromptOptions.forceSystemPrompt = %v, want the first handler's prompt", seen)
	}
	if result == nil || result.SystemPromptOptions == nil || result.SystemPromptOptions.ForceSystemPrompt == nil ||
		*result.SystemPromptOptions.ForceSystemPrompt != "forced by the first handler" || result.SystemPrompt == nil {
		t.Fatalf("result = %+v, want the shared options with the forced prompt and the returned systemPrompt", result)
	}
}

// runner.ts:1332-1364 and agent-session.ts: a handler that rewrites systemPromptOptions.selectedTools leaves the shared options holding its list, and the combined result reports
// the edit; a list equal to the base one is no edit.
func TestBeforeAgentStartReportsAHandlerEditToSelectedTools(t *testing.T) {
	emit := func(raw string, withMessage bool) (*extension.BeforeAgentStartCombinedResult, error) {
		runner := NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {
			func(args ...any) (any, error) {
				extension.SetBeforeAgentStartSelectedTools(args[1].(context.Context), json.RawMessage(raw))
				if withMessage {
					return &extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "note"}}, nil
				}
				return nil, nil
			},
		}}}}, t.TempDir())
		return runner.EmitBeforeAgentStart(t.Context(), extension.UserPromptTrigger("p", nil), extension.BuildSystemPromptOptions{SelectedTools: []string{"read"}})
	}

	result, err := emit(`["write","edit"]`, false)
	if err != nil || result == nil || result.SystemPromptOptions == nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !slices.Equal(result.SystemPromptOptions.SelectedTools, []string{"write", "edit"}) || !result.SelectedToolsEdited {
		t.Fatalf("selectedTools = %v edited = %v, want [write edit] and an edit", result.SystemPromptOptions.SelectedTools, result.SelectedToolsEdited)
	}

	result, err = emit(`["read"]`, false)
	if err != nil || result != nil {
		t.Fatalf("an unchanged selection returned %+v (err %v), want no result", result, err)
	}

	result, err = emit(`["read"]`, true)
	if err != nil || result == nil || result.SelectedToolsEdited || len(result.Messages) != 1 {
		t.Fatalf("an unchanged selection with a message returned %+v (err %v), want the message and no selection edit", result, err)
	}
}
