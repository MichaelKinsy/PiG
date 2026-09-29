package coding

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func runPromptTool(t *testing.T, name string, execute func()) agent.AgentTool {
	t.Helper()
	tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: name, Label: name, Description: name + " description", PromptSnippet: name + " prompt snippet", Parameters: json.RawMessage(`{"type":"object","properties":{}}`), Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		if execute != nil {
			execute()
		}
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: name}}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func runPromptSystemPatches(session *Session) []ai.OrderedSections {
	var patches []ai.OrderedSections
	for _, message := range session.Messages() {
		if message.System != nil {
			patches = append(patches, message.System.Sections)
		}
	}
	return patches
}

// agent-session.ts:693-709 rebuilds each subsequent turn from the run options with the live active tools, so a tool-triggered setActiveTools during a run with per-run sections reaches the provider prompt, the transcript and session.systemPrompt.
func TestRunSectionsFollowLiveActiveToolsOnNextTurn(t *testing.T) {
	var session *Session
	tools := []agent.AgentTool{
		runPromptTool(t, "first", func() { session.SetActiveToolsByName([]string{"second"}) }),
		runPromptTool(t, "second", nil),
	}
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		options := args[0].(extension.BeforeAgentStartEvent).SystemPromptOptions
		*options.Sections = append(*options.Sections, ai.PromptSection{Name: "plan_mode", Value: new("Plan only.")})
		return nil, nil
	}}}}
	var providerPrompts, sessionPrompts []string
	capture := func(request []ai.Message) {
		providerPrompts = append(providerPrompts, ai.GetCurrentSystemPrompt(request))
		sessionPrompts = append(sessionPrompts, session.SystemPrompt())
	}
	h := newRecoveryHarness(t, harnessOptions{tools: tools, extension: ext}, func(request []ai.Message) *ai.AssistantMessage {
		capture(request)
		return fauxToolCall("first")(request)
	}, func(request []ai.Message) *ai.AssistantMessage {
		capture(request)
		return fauxReply("done", ai.StopReasonStop, 0)(request)
	})
	session = h.session
	session.SetActiveToolsByName([]string{"first"})
	if _, err := session.Prompt(t.Context(), "start"); err != nil {
		t.Fatal(err)
	}
	if len(providerPrompts) != 2 || !reflect.DeepEqual(providerPrompts, sessionPrompts) {
		t.Fatalf("provider prompts=%q Session prompts=%q", providerPrompts, sessionPrompts)
	}
	if !strings.Contains(providerPrompts[0], "first prompt snippet") || !strings.Contains(providerPrompts[1], "second prompt snippet") || strings.Contains(providerPrompts[1], "first prompt snippet") {
		t.Fatalf("tool section did not follow live tools: %q", providerPrompts)
	}
	for _, prompt := range providerPrompts {
		if !strings.Contains(prompt, "<plan_mode>\nPlan only.\n</plan_mode>") {
			t.Fatalf("run section lost: %q", prompt)
		}
	}
	patches := runPromptSystemPatches(session)
	if len(patches) != 2 || ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: patches[0]}}) == "" {
		t.Fatalf("patches=%+v", patches)
	}
	var tools2 *ai.PromptSection
	for i := range patches[1] {
		if patches[1][i].Name == "tools" {
			tools2 = &patches[1][i]
		}
	}
	if tools2 == nil || tools2.Value == nil || !strings.Contains(*tools2.Value, "second prompt snippet") {
		t.Fatalf("tool patch not persisted: %+v", patches[1])
	}
}

// system-prompt-updates.test.ts:114-168: a handler that edits sections and returns systemPrompt in the same run still records the structured section patch; only the request is projected with the forced text.
func TestForcedPromptKeepsSameHandlerSectionEdits(t *testing.T) {
	turn := 0
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		turn++
		if turn == 3 {
			options := args[0].(extension.BeforeAgentStartEvent).SystemPromptOptions
			*options.Sections = append(*options.Sections, ai.PromptSection{Name: "plan_mode", Value: new("Plan only.")})
		}
		if turn == 2 || turn == 3 {
			return &extension.BeforeAgentStartEventResult{SystemPrompt: new("Exact prompt.")}, nil
		}
		return nil, nil
	}}}}
	var requests [][]ai.Message
	var responses []scriptedResponse
	for _, text := range []string{"one", "two", "three", "four"} {
		responses = append(responses, func(request []ai.Message) *ai.AssistantMessage {
			requests = append(requests, request)
			return fauxReply(text, ai.StopReasonStop, 0)(request)
		})
	}
	h := newRecoveryHarness(t, harnessOptions{extension: ext}, responses...)
	for _, text := range []string{"one", "two", "three", "four"} {
		if _, err := h.session.Prompt(t.Context(), text); err != nil {
			t.Fatal(err)
		}
	}
	counts := make([]int, len(requests))
	for i, request := range requests {
		for _, message := range request {
			if _, ok := message.(ai.SystemMessage); ok {
				counts[i]++
			}
		}
	}
	if !reflect.DeepEqual(counts, []int{1, 1, 1, 3}) || ai.GetCurrentSystemPrompt(requests[2]) != "Exact prompt." {
		t.Fatalf("system counts=%v forced=%q", counts, ai.GetCurrentSystemPrompt(requests[2]))
	}
	patches := runPromptSystemPatches(h.session)
	if len(patches) != 3 || len(patches[1]) != 1 || patches[1][0].Name != "plan_mode" || patches[1][0].Value == nil || *patches[1][0].Value != "<plan_mode>\nPlan only.\n</plan_mode>" || len(patches[2]) != 1 || patches[2][0].Name != "plan_mode" || patches[2][0].Value != nil {
		t.Fatalf("patches=%+v", patches)
	}
	if got := ai.GetCurrentSystemPrompt(messagesAsAI(h.session.Messages())); got != h.session.SystemPrompt() {
		t.Fatalf("transcript prompt %q != Session prompt %q", got, h.session.SystemPrompt())
	}
}

func messagesAsAI(messages []agent.AgentMessage) []ai.Message {
	var out []ai.Message
	for _, message := range messages {
		if message.System != nil {
			out = append(out, *message.System)
		}
	}
	return out
}

// agent-session.ts:1702-1714,1747 with 1409-1415: an explicit before_agent_start selectedTools edit becomes the executable and provider tool loadout for that request; without an edit a setActiveTools call inside the handler stays authoritative.
func TestBeforeAgentStartSelectedToolsControlLoadout(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*extension.BuildSystemPromptOptions)
	}{
		{"edited options", func(options *extension.BuildSystemPromptOptions) { options.SelectedTools[0] = "second" }},
		{"replaced options", func(options *extension.BuildSystemPromptOptions) {
			options.SelectedTools = []string{"second", "second", "missing"}
		}},
		{"live setActiveTools", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var session *Session
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
				event := args[0].(extension.BeforeAgentStartEvent)
				if event.Prompt != "two" {
					return nil, nil
				}
				if tc.edit != nil {
					tc.edit(extension.BeforeAgentStartOptions(args[1].(context.Context)))
				} else {
					session.SetActiveToolsByName([]string{"second"})
				}
				return nil, nil
			}}}}
			// Only setActiveTools rebuilds the base prompt (agent-session.ts:1385); an edit leaves the idle prompt at the base options (:1239-1241,1485).
			var bind func(*Session)
			if tc.edit == nil {
				bind = func(s *Session) { session = s }
			}
			assertSelectedToolsLoadout(t, ext, bind)
		})
	}
}

// A subprocess handler's selectedTools edit crosses the extension wire into the same Session loadout as a native edit.
func TestSubprocessBeforeAgentStartSelectedToolsControlLoadout(t *testing.T) {
	t.Run("fused-go", func(t *testing.T) {
		ext := sdk.New("selected-tools")
		ext.OnEvent("before_agent_start", func(_ sdk.Context, data map[string]any) (any, error) {
			if data["prompt"] == "two" {
				data["systemPromptOptions"].(map[string]any)["selectedTools"] = []any{"second"}
			}
			return nil, nil
		})
		host := subprocess.NewHost(t.TempDir())
		t.Cleanup(func() { host.Shutdown("test complete") })
		loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "selected-tools", Enabled: true}, ext.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		assertSelectedToolsLoadout(t, *loaded, nil)
	})
	t.Run("node", func(t *testing.T) {
		if _, err := exec.LookPath("node"); err != nil {
			t.Fatalf("node is required: %v", err)
		}
		source := filepath.Join(t.TempDir(), "index.mjs")
		if err := os.WriteFile(source, []byte(`export default function (pi) { pi.on("before_agent_start", async (event) => { await new Promise((resolve) => setTimeout(resolve, 20)); if (event.prompt === "two") event.systemPromptOptions.selectedTools.splice(0, 1, "second"); }); }`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		host := subprocess.NewHost(t.TempDir())
		t.Cleanup(func() { host.Shutdown("test complete") })
		loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "selected-tools", Source: source, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		assertSelectedToolsLoadout(t, *loaded, nil)
	})
}

// assertSelectedToolsLoadout prompts "one" and "two" with "first" active. ext must make "second" the loadout while handling "two"; bind receives the Session before the first prompt.
func assertSelectedToolsLoadout(t *testing.T, ext extension.Extension, bind func(*Session)) {
	t.Helper()
	executed := ""
	tools := []agent.AgentTool{
		runPromptTool(t, "first", func() { executed = "first" }),
		runPromptTool(t, "second", func() { executed = "second" }),
	}
	var session *Session
	var providerTools [][]string
	var providerPrompts, getterPrompts []string
	capture := func(request []ai.Message) {
		var names []string
		for _, tool := range ai.GetCurrentTools(request) {
			names = append(names, tool.Name)
		}
		providerTools = append(providerTools, names)
		providerPrompts = append(providerPrompts, ai.GetCurrentSystemPrompt(request))
		getterPrompts = append(getterPrompts, session.SystemPrompt())
	}
	h := newRecoveryHarness(t, harnessOptions{tools: tools, extension: ext}, func(request []ai.Message) *ai.AssistantMessage {
		capture(request)
		return fauxReply("one", ai.StopReasonStop, 0)(request)
	}, func(request []ai.Message) *ai.AssistantMessage {
		capture(request)
		return fauxToolCall("second")(request)
	}, func(request []ai.Message) *ai.AssistantMessage {
		capture(request)
		return fauxReply("done", ai.StopReasonStop, 0)(request)
	})
	session = h.session
	if bind != nil {
		bind(session)
	}
	session.SetActiveToolsByName([]string{"first"})
	basePrompt := session.SystemPrompt()
	for _, text := range []string{"one", "two"} {
		if _, err := session.Prompt(t.Context(), text); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(providerTools, [][]string{{"first"}, {"second"}, {"second"}}) || executed != "second" || !reflect.DeepEqual(session.ActiveToolNames(), []string{"second"}) {
		t.Fatalf("provider tools=%v executed=%q active=%v", providerTools, executed, session.ActiveToolNames())
	}
	if !reflect.DeepEqual(getterPrompts, providerPrompts) {
		t.Fatalf("run getters=%q provider prompts=%q", getterPrompts, providerPrompts)
	}
	if bind == nil && session.SystemPrompt() != basePrompt {
		t.Fatalf("per-run tool edit changed idle prompt: %q", session.SystemPrompt())
	}
	if len(providerPrompts) != 3 || !strings.Contains(providerPrompts[1], "second prompt snippet") || strings.Contains(providerPrompts[1], "first prompt snippet") {
		t.Fatalf("provider prompts=%q", providerPrompts)
	}
}

// agent-session.ts:1409-1419 applies the deduplicated selection before buildSystemPromptSections validates the section names, so a prompt rejected for an invalid section still leaves the handler's loadout active and sends no request.
func TestInvalidSectionRejectionKeepsAdmittedLoadout(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit bool
		want []string
	}{
		{"edited", true, []string{"second"}},
		{"unedited", false, []string{"first"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
				options := extension.BeforeAgentStartOptions(args[1].(context.Context))
				if tc.edit {
					options.SelectedTools = []string{"second", "second", "missing"}
				}
				*options.Sections = append(*options.Sections, ai.PromptSection{Name: "preamble", Value: new("invalid")})
				return nil, nil
			}}}}
			requests := 0
			h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{runPromptTool(t, "first", func() {}), runPromptTool(t, "second", func() {})}, extension: ext}, func(request []ai.Message) *ai.AssistantMessage {
				requests++
				return fauxReply("unexpected", ai.StopReasonStop, 0)(request)
			})
			h.session.SetActiveToolsByName([]string{"first", "first"})
			_, err := h.session.Prompt(t.Context(), "bad")
			if err == nil || err.Error() != "Invalid system prompt section name: preamble" {
				t.Fatalf("err=%v", err)
			}
			if got := h.session.ActiveToolNames(); !reflect.DeepEqual(got, tc.want) || requests != 0 {
				t.Fatalf("active=%v requests=%d, want %v and none", got, requests, tc.want)
			}
		})
	}
}
