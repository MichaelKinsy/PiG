package harness

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 42a3497d harness/agent.ts and types.ts: ToolRegistration.callers (who may call a tool: the model, other tools
// through executeTool(), or both, the default) and the stored pi.agent field `modelTools`, which filters the enabled
// tools the model is offered and never widens.

func callersTool(name string, callers ...durable.ToolCaller) *durable.ToolRegistration {
	return registryTool(name, func(tool *durable.ToolRegistration) { tool.Callers = callers })
}

func TestAgentResolutionSplitsModelToolsFromCallableTools(t *testing.T) {
	both := callersTool("both")
	model := callersTool("script", durable.CallerModel)
	tools := callersTool("hidden", durable.CallerTools)
	registry := CreateRegistry()
	mustInstall(t, registry, new(durable.Extension{Name: "x", Tools: []*durable.ToolRegistration{both, model, tools}}))
	snapshot := registry.Snapshot()

	agent := resolveWith(nil, snapshot, nil, nil)
	expectStrings(t, toolNamesOf(agent.Tools), []string{"both", "script"})
	expectStrings(t, toolNamesOf(agent.Callable), []string{"both", "hidden"})

	// `tools` enables first; `modelTools` then selects among the enabled tools the model may call, and never widens.
	enabled := &AgentState{Tools: &ToolSelection{Exact: true, Names: []string{"hidden", "script"}}}
	agent = resolveWith(enabled, snapshot, nil, nil)
	expectStrings(t, toolNamesOf(agent.Tools), []string{"script"})
	expectStrings(t, toolNamesOf(agent.Callable), []string{"hidden"})

	narrowed := &AgentState{ModelTools: &ToolSelection{Exact: true, Names: []string{"script", "hidden", "missing", "both", "script"}}}
	agent = resolveWith(narrowed, snapshot, nil, nil)
	expectStrings(t, toolNamesOf(agent.Tools), []string{"script", "both"})
	expectStrings(t, toolNamesOf(agent.Callable), []string{"both", "hidden"})

	removed := &AgentState{ModelTools: &ToolSelection{Remove: []string{"both"}}}
	agent = resolveWith(removed, snapshot, nil, nil)
	expectStrings(t, toolNamesOf(agent.Tools), []string{"script"})
	expectStrings(t, toolNamesOf(agent.Callable), []string{"both", "hidden"})
}

// addTools applies to `tools` and `modelTools` (agent.ts addTools): an array gets each name it lacks appended, {remove}
// loses the names, and an unset filter writes nothing.
func TestAddToolsExtendsToolsAndModelTools(t *testing.T) {
	setup := chatSetup(t)
	grow := tlTool("grow", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{AddTools: []string{"extra", "gone"}}}, nil
	})
	extra, gone := tlTool("extra", tlNoContent), tlTool("gone", tlNoContent)
	addTool(t, setup.Registry, grow)
	addTool(t, setup.Registry, extra)
	addTool(t, setup.Registry, gone)
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"grow", map[string]any{}, "c1"}), tlDone()}, func(_ Harness, root Conversation) {
		mustConfigure(t, root, AgentChange{
			Tools:      SetTo(ToolChange{List: []*durable.ToolRegistration{grow}}),
			ModelTools: SetTo(ToolChange{Edit: &ToolEdit{Remove: []*durable.ToolRegistration{gone, extra}}}),
		})
	})
	state, err := durable.Snapshot[AgentState](testContext, run.harness, AgentDoc, run.root.Id())
	if err != nil || state == nil || state.Tools == nil || state.ModelTools == nil {
		t.Fatalf("stored state %+v, %v", state, err)
	}
	expectStrings(t, state.Tools.Names, []string{"grow", "extra", "gone"})
	if len(state.ModelTools.Remove) != 0 {
		t.Fatalf("modelTools.remove = %v, want the added names taken out", state.ModelTools.Remove)
	}
	// A load also writes the names to add, which offers a deferred tool and has no effect on another.
	expectStrings(t, state.ModelTools.Add, []string{"extra", "gone"})
	got, err := json.Marshal(state)
	if err != nil || string(got) != `{"model":{"provider":"faux","modelId":"faux-1"},"tools":["grow","extra","gone"],"modelTools":{"add":["extra","gone"]}}` {
		t.Fatalf("stored state = %s, %v", got, err)
	}
	mustClose(t, run.harness)
}

// Pi 77332f7a: modelTools is a list of names or `{ remove?, add? }`; an absent member stays absent.
func TestToolSelectionJSONRoundTripsRemoveAndAdd(t *testing.T) {
	for _, text := range []string{`["a","b"]`, `[]`, `{"remove":["a"]}`, `{"add":["a"]}`, `{"remove":["a"],"add":["b","c"]}`, `{"add":[]}`, `{}`} {
		var selection ToolSelection
		if err := json.Unmarshal([]byte(text), &selection); err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		got, err := json.Marshal(selection)
		if err != nil || string(got) != text {
			t.Errorf("%s round-trips to %s, %v", text, got, err)
		}
	}
}
