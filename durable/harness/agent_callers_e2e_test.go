package harness

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi 1.2.0 test/harness-callers.test.ts: who may call a tool, end to end through a conversation: the tools the model is
// offered, the tools callable by tools, nested calls of each, modelTools and addTools.

// ranTool is a tool that answers "<name> ran" (`tool` of the upstream test).
func ranTool(name string, callers ...durable.ToolCaller) *durable.ToolRegistration {
	return tlTool(name, func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: name + " ran"}}}, nil
	}, func(registration *durable.ToolRegistration) { registration.Callers = callers })
}

// prober is a tool that calls each of names as a nested call and records what it got back (`prober` of the upstream test).
func prober(names ...string) (*durable.ToolRegistration, map[string]durable.NestedToolExecutionResult) {
	received := map[string]durable.NestedToolExecutionResult{}
	return tlTool("probe", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		for _, name := range names {
			result, err := api.ExecuteTool(ctx, name, map[string]any{})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			received[name] = result
		}
		return durable.ToolExecutionResult{}, nil
	}), received
}

func offeredAndCallable(t *testing.T, root Conversation) (offered, callable []string) {
	t.Helper()
	agent := must(root.Agent(testContext))
	return toolNamesOf(agent.Tools), toolNamesOf(agent.Callable)
}

func expectNames(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func modelToolsOf(t *testing.T, harness Harness, root Conversation) string {
	t.Helper()
	state := must(durable.Snapshot(testContext, harness, AgentDoc, root.Id()))
	if state == nil || state.ModelTools == nil {
		return "none"
	}
	return encodedJSON(t, state.ModelTools)
}

func openCallersChat(t *testing.T, tools ...*durable.ToolRegistration) *chatState {
	t.Helper()
	setup := chatSetup(t)
	for _, tool := range tools {
		addTool(t, setup.Registry, tool)
	}
	return setup
}

// harness-callers.test.ts:61 it("offers the model only tools it may call, and lets tools call only tools they may call")
func TestTheModelIsOfferedOnlyToolsItMayCallAndToolsCallOnlyToolsTheyMayCall(t *testing.T) {
	script, hidden, both := ranTool("script", durable.CallerModel), ranTool("hidden", durable.CallerTools), ranTool("both")
	probe, received := prober("script", "hidden", "both")
	setup := openCallersChat(t, script, hidden, both, probe)
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	offered, callable := offeredAndCallable(t, root)
	expectNames(t, "offered", offered, []string{"script", "both", "probe"})
	expectNames(t, "callable", callable, []string{"hidden", "both", "probe"})
	if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if codes := diagnosticCodes(received["script"]); len(codes) == 0 || codes[0] != "tool_unavailable" {
		t.Errorf("script call = %+v, want tool_unavailable", received["script"])
	}
	for _, name := range []string{"hidden", "both"} {
		if got := received[name]; got.IsError || got.StructuredOutput != name+" ran" {
			t.Errorf("%s call = %+v, want %q", name, got, name+" ran")
		}
	}
	mustClose(t, harness)
}

// harness-callers.test.ts:78 it("narrows what the model is offered with modelTools, leaving the rest callable by tools")
func TestModelToolsNarrowWhatTheModelIsOfferedLeavingTheRestCallableByTools(t *testing.T) {
	read, bash := ranTool("read"), ranTool("bash")
	probe, received := prober("read", "bash")
	setup := openCallersChat(t, read, bash, probe)
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	if err := root.Configure(testContext, AgentChange{ModelTools: SetTo(ToolChange{List: []*durable.ToolRegistration{probe}})}); err != nil {
		t.Fatal(err)
	}
	offered, callable := offeredAndCallable(t, root)
	expectNames(t, "offered", offered, []string{"probe"})
	expectNames(t, "callable", callable, []string{"read", "bash", "probe"})
	if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if received["read"].IsError || received["bash"].IsError || len(received) != 2 {
		t.Errorf("received %+v, want both calls to succeed", received)
	}

	if err := root.Configure(testContext, AgentChange{ModelTools: SetTo(ToolChange{Edit: &ToolEdit{Remove: []*durable.ToolRegistration{bash}}})}); err != nil {
		t.Fatal(err)
	}
	offered, _ = offeredAndCallable(t, root)
	expectNames(t, "offered after remove", offered, []string{"read", "probe"})
	if got := modelToolsOf(t, harness, root); got != `{"remove":["bash"]}` {
		t.Errorf("modelTools = %s, want {remove: [bash]}", got)
	}
	if err := root.Configure(testContext, AgentChange{ModelTools: Cleared[ToolChange]()}); err != nil {
		t.Fatal(err)
	}
	offered, _ = offeredAndCallable(t, root)
	expectNames(t, "offered after clearing", offered, []string{"read", "bash", "probe"})
	mustClose(t, harness)
}

// harness-callers.test.ts:100 it("never offers a tool that is not enabled, nor lets tools call it")
func TestAToolThatIsNotEnabledIsNeverOfferedNorCallableByTools(t *testing.T) {
	read, bash := ranTool("read"), ranTool("bash")
	probe, received := prober("bash")
	setup := openCallersChat(t, read, bash, probe)
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	// modelTools cannot bring back what tools disabled.
	if err := root.Configure(testContext, AgentChange{
		Tools:      SetTo(ToolChange{Edit: &ToolEdit{Remove: []*durable.ToolRegistration{bash}}}),
		ModelTools: SetTo(ToolChange{List: []*durable.ToolRegistration{bash, probe}}),
	}); err != nil {
		t.Fatal(err)
	}
	offered, callable := offeredAndCallable(t, root)
	expectNames(t, "offered", offered, []string{"probe"})
	expectNames(t, "callable", callable, []string{"read", "probe"})
	if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if codes := diagnosticCodes(received["bash"]); len(codes) == 0 || codes[0] != "tool_unavailable" {
		t.Errorf("bash call = %+v, want tool_unavailable", received["bash"])
	}
	mustClose(t, harness)
}

// harness-callers.test.ts:116 it("answers a model-issued call of a tool it was not offered as unavailable")
func TestAModelIssuedCallOfAToolItWasNotOfferedIsUnavailable(t *testing.T) {
	read, bash := ranTool("read"), ranTool("bash")
	setup := openCallersChat(t, read, bash)
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"bash", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	if err := root.Configure(testContext, AgentChange{ModelTools: SetTo(ToolChange{List: []*durable.ToolRegistration{read}})}); err != nil {
		t.Fatal(err)
	}
	if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
		t.Fatal(err)
	}
	results := tlResults(allEntries(t, root))
	if len(results) != 1 || !strings.Contains(tlResultText(results[0]), "Tool bash is not available") {
		t.Fatalf("results %+v, want one saying Tool bash is not available", results)
	}
	mustClose(t, harness)
}

// harness-callers.test.ts:130 it("applies addTools to modelTools too, so the model is offered the added tools")
func TestAddToolsAppliesToModelToolsSoTheModelIsOfferedTheAddedTools(t *testing.T) {
	extra := ranTool("extra")
	grow := tlTool("grow", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{AddTools: []string{"extra"}}}, nil
	})
	setup := openCallersChat(t, extra, grow)
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"grow", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	if err := root.Configure(testContext, AgentChange{ModelTools: SetTo(ToolChange{List: []*durable.ToolRegistration{grow}})}); err != nil {
		t.Fatal(err)
	}
	if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if got := modelToolsOf(t, harness, root); got != `["grow","extra"]` {
		t.Errorf("modelTools = %s, want [grow, extra]", got)
	}
	offered, _ := offeredAndCallable(t, root)
	expectNames(t, "offered", offered, []string{"grow", "extra"})
	mustClose(t, harness)
}
