// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package driver_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports the execution half of harness-registry.test.ts:189 "replaces same-name tools in place, wraps the winner, then applies the
// filter": the registry and resolveAgent themselves are host-side Go shared with the in-process Harness (covered by
// durable/harness/harness_registry_test.go); what the driver owns is which registration a tool effect executes. generation/tool.ts
// resolves the called tool among its phase agent's tools, so the later same-name tool replaces the earlier and wrappers apply.
type toolLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *toolLog) add(line string) { l.mu.Lock(); l.lines = append(l.lines, line); l.mu.Unlock() }
func (l *toolLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func loggedTool(log *toolLog, name, description, line string) *durable.ToolRegistration {
	tool := conversationTool(name)
	tool.Description = description
	tool.Execute = func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		log.add(line)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
	}
	return tool
}

func runToolTurn(t *testing.T, setup *chat, tool string) {
	t.Helper()
	setup.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(tool, map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		fauxAnswer("done"),
	})
	_, root, _ := chatOpen(t, setup)
	settled, err := submitInput(t, root, "go").Wait(t.Context())
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("settled = %+v, %v", settled, err)
	}
}

func TestCoreRegistryExecutesTheWinnerOfASameNameToolThroughItsWrappers(t *testing.T) {
	log := &toolLog{}
	setup := chatSetup()
	coding := &durable.Extension{Name: "coding", Tools: []*durable.ToolRegistration{loggedTool(log, "read", "read tool", "coding read"), loggedTool(log, "bash", "bash tool", "coding bash")}}
	venv := &durable.Extension{Name: "venv", Tools: []*durable.ToolRegistration{loggedTool(log, "bash", "venv bash", "venv bash")}}
	timing := &durable.Extension{Name: "timing", Wraps: []durable.Wrap{
		harness.WrapTool(conversationTool("bash"), func(inner *durable.ToolRegistration) *durable.ToolRegistration {
			wrapped := *inner
			wrapped.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				log.add("timed")
				return inner.Execute(ctx, args, api)
			}
			return &wrapped
		}),
	}}
	for _, extension := range []*durable.Extension{coding, venv, timing} {
		if err := setup.registry.Install(extension); err != nil {
			t.Fatal(err)
		}
	}
	runToolTurn(t, setup, "bash")
	if got, want := log.all(), []string{"timed", "venv bash"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("executed %v, want %v: the later same-name tool replaces the earlier and the wrapper applies", got, want)
	}
}

// tool.ts call phase: a call to a tool the conversation's agent filtered out settles as a tool_unavailable result, the run goes on, and
// the tool does not execute.
func TestCoreRegistryAnswersACallToAFilteredOutToolWithoutExecutingIt(t *testing.T) {
	log := &toolLog{}
	setup := chatSetup()
	if err := setup.registry.Install(&durable.Extension{Name: "coding", Tools: []*durable.ToolRegistration{loggedTool(log, "read", "read tool", "read"), loggedTool(log, "bash", "bash tool", "bash")}}); err != nil {
		t.Fatal(err)
	}
	setup.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("bash", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		fauxAnswer("done"),
	})
	_, root, _ := chatOpen(t, setup)
	conversationConfigure(t, root, harness.AgentChange{Tools: harness.SetTo(harness.ToolChange{Edit: &harness.ToolEdit{Remove: []*durable.ToolRegistration{conversationTool("bash")}}})})
	settled, err := submitInput(t, root, "go").Wait(t.Context())
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("settled = %+v, %v", settled, err)
	}
	if got := log.all(); len(got) != 0 {
		t.Fatalf("executed %v, want nothing", got)
	}
	var results []string
	for _, entry := range chatEntries(t, root) {
		if entry.Kind != durable.ToolResultEntry.Kind {
			continue
		}
		for _, message := range entry.Model {
			if text, ok := message.(ai.ToolResultMessage); ok {
				results = append(results, text.ToolName)
				if !text.IsError {
					t.Fatalf("result %+v, want an error result", text)
				}
			}
		}
	}
	if !reflect.DeepEqual(results, []string{"bash"}) {
		t.Fatalf("tool results %v, want one error result for bash", results)
	}
}

func promptText(text string) func(context.Context, durable.PromptInput) (*string, error) {
	return func(context.Context, durable.PromptInput) (*string, error) { return &text, nil }
}

// harness-registry.test.ts:250 "orders sections by extension, replaces same keys in place, and renders instructions last and unwrapped",
// through the system entry a real turn records (prompt.ts :45 wraps every section except tag:false as <key>...</key>): the driver resolves sections for the generation request (Session.agent).
func TestCoreRegistryOrdersAndWrapsPromptSectionsInTheRecordedSystemEntry(t *testing.T) {
	setup := chatSetup()
	coding := &durable.Extension{Name: "coding", Sections: []*durable.PromptSection{harness.Section("preamble", promptText("You code."), harness.SectionOptions{Tag: new(false)}), harness.Section("cwd", promptText("/repo"))}}
	skills := &durable.Extension{Name: "skills", Sections: []*durable.PromptSection{harness.Section("skills", promptText("S"))}}
	override := &durable.Extension{Name: "override", Sections: []*durable.PromptSection{harness.Section("preamble", promptText("You review."), harness.SectionOptions{Tag: new(false)})},
		Wraps: []durable.Wrap{
			harness.WrapSection("cwd", func(inner *durable.PromptSection) *durable.PromptSection {
				wrapped := *inner
				wrapped.Render = func(ctx context.Context, in durable.PromptInput) (*string, error) {
					text, err := inner.Render(ctx, in)
					if err != nil || text == nil {
						return text, err
					}
					return new(*text + "!"), nil
				}
				return &wrapped
			}),
			// Instructions are not wrapped.
			harness.WrapSection("instructions", func(*durable.PromptSection) *durable.PromptSection { panic(errors.New("never")) }),
		}}
	for _, e := range []*durable.Extension{coding, skills, override} {
		if err := setup.registry.Install(e); err != nil {
			t.Fatal(err)
		}
	}
	setup.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
	_, root, _ := chatOpen(t, setup)
	conversationConfigure(t, root, harness.AgentChange{Instructions: harness.SetTo("Be terse.")})
	if _, err := submitInput(t, root, "go").Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got [][2]string
	for _, entry := range chatEntries(t, root) {
		if entry.Kind != durable.SystemEntry.Kind {
			continue
		}
		for _, message := range entry.Model {
			system, ok := message.(ai.SystemMessage)
			if !ok {
				t.Fatalf("system entry model %T", message)
			}
			for _, section := range system.Sections {
				value := "<nil>"
				if section.Value != nil {
					value = *section.Value
				}
				got = append(got, [2]string{section.Name, value})
			}
		}
	}
	want := [][2]string{{"preamble", "You review."}, {"cwd", "<cwd>\n/repo!\n</cwd>"}, {"skills", "<skills>\nS\n</skills>"}, {"instructions", "<instructions>\nBe terse.\n</instructions>"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sections %q, want %q", got, want)
	}
}

// harness-registry.test.ts:279 "collects hooks of the selected extensions in extension order": the hooks a real run executes are those of the
// conversation's selected extensions, in the selection's order (agent.ts agentHooks), and an extension left out contributes none.
func TestCoreRegistryRunsTheHooksOfTheSelectedExtensionsInSelectionOrder(t *testing.T) {
	log := &toolLog{}
	setup := chatSetup()
	bash := loggedTool(log, "bash", "bash tool", "bash")
	beforeTool := func(name string) *harness.ToolHooks {
		return &harness.ToolHooks{BeforeTool: func(context.Context, harness.ToolHookCall, harness.HookApi) (*harness.BeforeToolResult, error) {
			log.add("beforeTool " + name)
			return nil, nil
		}}
	}
	a := &durable.Extension{Name: "a", Tools: []*durable.ToolRegistration{bash}, Hooks: []durable.HookRegistration{
		harness.Hook(harness.ToolTask, beforeTool("a")),
		harness.Hook(harness.GenerationTask, &harness.GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, harness.HookApi) (*harness.YieldContinue, error) {
			log.add("onYield a")
			return nil, nil
		}}),
	}}
	b := &durable.Extension{Name: "b", Hooks: []durable.HookRegistration{harness.Hook(harness.ToolTask, beforeTool("b"))}}
	for _, e := range []*durable.Extension{a, b} {
		if err := setup.registry.Install(e); err != nil {
			t.Fatal(err)
		}
	}
	call := func(id string) ai.FauxResponseStep {
		return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("bash", map[string]any{}, &ai.FauxToolCallOptions{ID: id})}, StopReason: "toolUse"})
	}
	setup.faux.SetResponses([]ai.FauxResponseStep{call("c1"), fauxAnswer("one"), call("c2"), fauxAnswer("two")})
	_, root, _ := chatOpen(t, setup)
	turn := func(text string) []string {
		t.Helper()
		before := len(log.all())
		if settled, err := submitInput(t, root, text).Wait(t.Context()); err != nil || settled.Status != durable.SubmissionDone {
			t.Fatalf("settled = %+v, %v", settled, err)
		}
		return log.all()[before:]
	}
	// The default selection is install order: a then b.
	if got, want := turn("one"), []string{"beforeTool a", "beforeTool b", "bash", "onYield a"}; !slices.Equal(got, want) {
		t.Fatalf("default selection ran %v, want %v", got, want)
	}
	// An exact selection runs in its own order, and a left-out extension runs nothing.
	conversationConfigure(t, root, harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{List: []*durable.Extension{b, a}})})
	if got, want := turn("two"), []string{"beforeTool b", "beforeTool a", "bash", "onYield a"}; !slices.Equal(got, want) {
		t.Fatalf("selection [b a] ran %v, want %v", got, want)
	}
	// Selecting only b drops a's tool, its beforeTool and its onYield.
	conversationConfigure(t, root, harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{List: []*durable.Extension{b}})})
	setup.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("three")})
	if got := turn("three"); len(got) != 0 {
		t.Fatalf("selection [b] ran %v, want no hooks of a", got)
	}
}
