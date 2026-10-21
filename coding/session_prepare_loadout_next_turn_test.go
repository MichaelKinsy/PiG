package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// twoTurnLoadoutRun runs a prompt whose first response calls `watch` and whose second answers, over a session that registers `watch` with hook and `other`. watched runs when the tool executes, between the two turns. It returns what each request declared and the system prompt it carried.
func twoTurnLoadoutRun(t *testing.T, hook extension.ToolPrepareLoadoutFunc, watched func()) (declared [][]string, prompts []string) {
	t.Helper()
	execute := func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		if watched != nil {
			watched()
		}
		return textResult("watched"), nil
	}
	watch := orchestrationTool("watch", "Watch.", extension.ToolDefinition{PromptSnippet: "watch snippet", PrepareLoadout: hook, Execute: execute})
	other := orchestrationTool("other", "Other.", extension.ToolDefinition{PromptSnippet: "other snippet", Execute: execute})
	session, faux := newOrchestrationSession(t, extension.Extension{Tools: map[string]extension.RegisteredTool{"watch": watch, "other": other}, ToolOrder: []string{"watch", "other"}})
	record := func(request ai.TranscriptContext) {
		var names []string
		for _, tool := range ai.GetCurrentTools(request.Messages()) {
			names = append(names, tool.Name+": "+tool.Description)
		}
		declared = append(declared, names)
		prompts = append(prompts, ai.GetCurrentSystemPrompt(request.Messages()))
	}
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			record(request)
			return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxToolCall("watch", map[string]any{}, &ai.FauxToolCallOptions{ID: "call_watch"})}, ai.FauxAssistantMessageOptions{StopReason: string(ai.StopReasonToolUse)}), nil
		}),
		ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			record(request)
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}.AssistantMessage(), nil
		}),
	})
	if err := session.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(declared) != 2 {
		t.Fatalf("the run made %d requests, want 2", len(declared))
	}
	return declared, prompts
}

// upstream agent-session.ts:899-933,1743-1755: prepareNextTurnWithContext calls _preparePromptAndToolLoadout, whose _applyToolLoadout runs every active tool's prepareLoadout hook before each later turn of a run, so the descriptions the hook sets track the state it reads.
func TestPrepareLoadoutRunsBeforeEveryTurnOfARun(t *testing.T) {
	var evaluations atomic.Int32
	declared, _ := twoTurnLoadoutRun(t, func(extension.ToolLoadout) *extension.ToolLoadoutChanges {
		n := evaluations.Add(1)
		return &extension.ToolLoadoutChanges{Descriptions: map[string]string{"watch": fmt.Sprintf("Watch, loadout evaluation %d.", n)}}
	}, nil)
	// The first request carries the evaluation of the prompt, the second a later one from before its turn. A re-declared description moves its tool to the end of the replayed list, so the declaration is found by name.
	description := func(request []string) string {
		for _, entry := range request {
			if strings.HasPrefix(entry, "watch: ") {
				return entry
			}
		}
		return ""
	}
	first, second := description(declared[0]), description(declared[1])
	if !strings.HasPrefix(first, "watch: Watch, loadout evaluation ") || !strings.HasPrefix(second, "watch: Watch, loadout evaluation ") || first == second {
		t.Fatalf("declarations per request = %q, want the second request to carry a later evaluation than the first", declared)
	}
}

// upstream agent-session.ts:1743-1755: the hidden declarations are rebuilt by _applyToolLoadout before each turn, and the tool list of the system prompt follows them (options.hiddenTools).
func TestPrepareLoadoutHiddenDeclarationsFollowStateBetweenTurns(t *testing.T) {
	var watched atomic.Bool
	declared, prompts := twoTurnLoadoutRun(t, func(extension.ToolLoadout) *extension.ToolLoadoutChanges {
		if !watched.Load() {
			return nil
		}
		return &extension.ToolLoadoutChanges{HiddenDeclarations: []string{"other"}}
	}, func() { watched.Store(true) })
	names := func(request []string) []string {
		var out []string
		for _, entry := range request {
			out = append(out, strings.SplitN(entry, ":", 2)[0])
		}
		slices.Sort(out)
		return out
	}
	if got := names(declared[0]); !slices.Equal(got, []string{"other", "watch"}) {
		t.Fatalf("first request declares %q, want other and watch", got)
	}
	if got := names(declared[1]); !slices.Equal(got, []string{"watch"}) {
		t.Fatalf("second request declares %q, want watch only: the hook, which reads state the tool changed, hid other before that turn", got)
	}
	if !strings.Contains(prompts[0], "other snippet") || strings.Contains(prompts[1], "other snippet") {
		t.Fatalf("system prompts should list other on the first request only:\n%q\n%q", prompts[0], prompts[1])
	}
}
