package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func context9789Harness(t *testing.T, ext extension.Extension) *recoveryHarness {
	t.Helper()
	h := newRecoveryHarness(t, harnessOptions{tools: tools.CreateCodingTools(t.TempDir(), nil, ""), extension: ext, settings: `{"compaction":{"keepRecentTokens":1}}`})
	h.session.SetActiveToolsByName([]string{"read", "bash", "edit", "write"})
	return h
}

func context9789Node(t *testing.T, source string) extension.Extension {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.mjs")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("context test complete") })
	ext, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "context-9789", Source: path, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return *ext
}

func context9789Capture(h *recoveryHarness, text string) func(*testing.T) []ai.Message {
	var request []ai.Message
	h.provider.responses = append(h.provider.responses, func(messages []ai.Message) *ai.AssistantMessage {
		request = messages
		return fauxReply(text, ai.StopReasonStop, 0)(messages)
	})
	return func(t *testing.T) []ai.Message {
		t.Helper()
		if request == nil {
			t.Fatal("expected provider request")
		}
		return request
	}
}

func context9789Prompt(t *testing.T, h *recoveryHarness, text string) {
	t.Helper()
	if _, err := h.session.Prompt(t.Context(), text); err != nil {
		t.Fatal(err)
	}
}

func context9789Roles(messages []ai.Message) []string {
	result := make([]string, len(messages))
	for i, m := range messages {
		switch m.(type) {
		case ai.SystemMessage:
			result[i] = "system"
		case ai.UserMessage:
			result[i] = "user"
		case ai.AssistantMessage:
			result[i] = "assistant"
		case ai.ToolResultMessage:
			result[i] = "toolResult"
		}
	}
	return result
}

func context9789ToolNames(messages []ai.Message) []string {
	var names []string
	for _, tool := range ai.GetCurrentTools(messages) {
		names = append(names, tool.Name)
	}
	return names
}

func context9789Slice(messages []extension.AgentMessage) []extension.AgentMessage {
	index := slices.IndexFunc(messages, func(m extension.AgentMessage) bool { return m.(agent.AgentMessage).Role() == "compactionSummary" })
	if index < 0 {
		index = max(0, len(messages)-1)
	} // Array.slice(-1) before compaction.
	return slices.Clone(messages[index:])
}

func context9789CompactionHook(ext *extension.Extension) {
	ext.Handlers["session_before_compact"] = []extension.HandlerFn{func(args ...any) (any, error) {
		raw, err := json.Marshal(args[0].(extension.SessionBeforeCompactEvent).Preparation)
		if err != nil {
			return nil, err
		}
		var prep struct {
			FirstKeptEntryID string `json:"firstKeptEntryId"`
			TokensBefore     int    `json:"tokensBefore"`
		}
		if err := json.Unmarshal(raw, &prep); err != nil {
			return nil, err
		}
		return extension.SessionBeforeCompactResult{Compaction: map[string]any{"summary": "extension summary", "firstKeptEntryId": prep.FirstKeptEntryID, "tokensBefore": prep.TokensBefore, "details": map[string]any{"source": "test"}}}, nil
	}}
}

func context9789Compact(t *testing.T, h *recoveryHarness) {
	t.Helper()
	h.provider.responses = []scriptedResponse{fauxReply("one", ai.StopReasonStop, 0), fauxReply("two", ai.StopReasonStop, 0)}
	context9789Prompt(t, h, "first")
	context9789Prompt(t, h, "second")
	if err := h.session.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	messages := h.session.Messages()
	if len(messages) < 2 || messages[0].Role() != "system" || messages[1].Role() != "compactionSummary" {
		t.Fatalf("compacted messages=%+v", messages)
	}
}

// Ports packages/coding-agent/test/suite/regressions/9789-context-handler-system-messages.test.ts:59.
func TestUpstream9789SliceRetainsPromptAndTools(t *testing.T) {
	var seen []extension.AgentMessage
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"context": {func(args ...any) (any, error) {
		seen = args[0].(extension.ContextEvent).Messages
		return &extension.ContextEventResult{Messages: context9789Slice(seen)}, nil
	}}}}
	context9789CompactionHook(&ext)
	h := context9789Harness(t, ext)
	context9789Compact(t, h)
	request := context9789Capture(h, "after compaction")
	context9789Prompt(t, h, "third")
	for _, m := range seen {
		if m.(agent.AgentMessage).Role() == "system" {
			t.Error("context handler received system message")
		}
	}
	roles := context9789Roles(request(t))
	if len(roles) == 0 || roles[0] != "system" || len(slices.DeleteFunc(slices.Clone(roles), func(s string) bool { return s != "system" })) != 1 {
		t.Fatalf("roles=%v", roles)
	}
	if got := context9789ToolNames(request(t)); !slices.Equal(got, h.session.ActiveToolNames()) {
		t.Errorf("tools=%v want=%v", got, h.session.ActiveToolNames())
	}
	if got := ai.GetCurrentSystemPrompt(request(t)); got != h.session.SystemPrompt() {
		t.Errorf("prompt=%q want=%q", got, h.session.SystemPrompt())
	}
}

// Ports packages/coding-agent/test/suite/regressions/9789-context-handler-system-messages.test.ts:87. The Node factory exercises in-place prompt-option mutation across the production wire.
func TestUpstream9789KeepsMidConversationSystems(t *testing.T) {
	ext := context9789Node(t, `export default function(pi){let turn=0;pi.on("before_agent_start",event=>{if(++turn===2)event.systemPromptOptions.sections.plan_mode="Plan only.";});pi.on("context",async event=>({messages:event.messages}));}`)
	h := context9789Harness(t, ext)
	var handlerErrors []string
	h.session.currentRunner().AddErrorListener(func(err *extension.ExtensionError) { handlerErrors = append(handlerErrors, err.Error) })
	h.provider.responses = []scriptedResponse{fauxReply("one", ai.StopReasonStop, 0)}
	context9789Prompt(t, h, "first")
	request := context9789Capture(h, "two")
	context9789Prompt(t, h, "second")
	var systems []ai.SystemMessage
	for _, m := range request(t) {
		if system, ok := m.(ai.SystemMessage); ok {
			systems = append(systems, system)
		}
	}
	if len(systems) != 2 {
		t.Fatalf("system messages=%d want=2", len(systems))
	}
	want := ai.OrderedSections{{Name: "plan_mode", Value: new("<plan_mode>\nPlan only.\n</plan_mode>")}}
	if !reflect.DeepEqual(systems[1].Sections, want) {
		t.Fatalf("sections=%v want=%v", systems[1].Sections, want)
	}
	if len(handlerErrors) != 0 {
		t.Fatalf("handler errors=%q", handlerErrors)
	}
	// system-prompt-updates.test.ts:114-171: a per-run section is recorded, not written back into the base options, and a later unmodified run records its removal.
	if base := h.session.GetSystemPromptOptions().Sections; base != nil && len(*base) != 0 {
		t.Fatalf("run mutated base sections: %+v", *base)
	}
	request = context9789Capture(h, "three")
	context9789Prompt(t, h, "third")
	systems = nil
	for _, message := range request(t) {
		if system, ok := message.(ai.SystemMessage); ok {
			systems = append(systems, system)
		}
	}
	if len(systems) != 3 || !reflect.DeepEqual(systems[2].Sections, ai.OrderedSections{{Name: "plan_mode"}}) {
		t.Fatalf("unmodified run lost section removal: %+v", systems)
	}
}

// Ports packages/coding-agent/test/suite/regressions/9789-context-handler-system-messages.test.ts:111. Use the exact Node splice rather than returning a replacement array, which would hide the missing mutation contract.
func TestUpstream9789InPlaceInsertionWithoutReturn(t *testing.T) {
	ext := context9789Node(t, `export default function(pi){pi.on("context",async event=>{event.messages.splice(0,0,{role:"user",content:[{type:"text",text:"injected"}],timestamp:0});});}`)
	h := context9789Harness(t, ext)
	request := context9789Capture(h, "done")
	context9789Prompt(t, h, "hello")
	if got := context9789Roles(request(t)); !slices.Equal(got, []string{"system", "user", "user"}) {
		t.Errorf("roles=%v", got)
	}
	if got := context9789ToolNames(request(t)); !slices.Equal(got, h.session.ActiveToolNames()) {
		t.Errorf("tools=%v want=%v", got, h.session.ActiveToolNames())
	}
}

// Ports packages/coding-agent/test/suite/regressions/9789-context-handler-system-messages.test.ts:135.
func TestUpstream9789AddedSystemAfterReplayedHead(t *testing.T) {
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"context": {func(args ...any) (any, error) {
		event := args[0].(extension.ContextEvent)
		return &extension.ContextEventResult{Messages: append([]extension.AgentMessage{agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("ephemeral reminder"), Timestamp: 0}}}, event.Messages...)}, nil
	}}}}
	h := context9789Harness(t, ext)
	request := context9789Capture(h, "done")
	context9789Prompt(t, h, "hello")
	if got := context9789Roles(request(t)); !slices.Equal(got, []string{"system", "system", "user"}) {
		t.Errorf("roles=%v", got)
	}
	if got := context9789ToolNames(request(t)); !slices.Equal(got, h.session.ActiveToolNames()) {
		t.Errorf("tools=%v want=%v", got, h.session.ActiveToolNames())
	}
	prompt := ai.GetCurrentSystemPrompt(request(t))
	if !strings.Contains(prompt, h.session.SystemPrompt()) || !strings.Contains(prompt, "ephemeral reminder") {
		t.Errorf("prompt=%q", prompt)
	}
}

// Ports packages/coding-agent/test/suite/regressions/9789-context-handler-system-messages.test.ts:165.
func TestUpstream9789ContextWithSystemFollowsContext(t *testing.T) {
	var seen []extension.AgentMessage
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"context_with_system": {func(args ...any) (any, error) {
			seen = args[0].(extension.ContextWithSystemEvent).Messages
			messages := slices.Clone(seen)
			for i, m := range messages {
				message := m.(agent.AgentMessage)
				if message.System != nil && message.System.ToolsAdded != nil {
					system := *message.System
					system.ToolsAdded = slices.DeleteFunc(slices.Clone(system.ToolsAdded), func(tool ai.ToolSchema) bool { return tool.Name == "bash" })
					message.System = &system
					messages[i] = message
				}
			}
			return &extension.ContextEventResult{Messages: messages}, nil
		}},
		"context": {func(args ...any) (any, error) {
			return &extension.ContextEventResult{Messages: context9789Slice(args[0].(extension.ContextEvent).Messages)}, nil
		}},
	}}
	context9789CompactionHook(&ext)
	h := context9789Harness(t, ext)
	context9789Compact(t, h)
	request := context9789Capture(h, "after compaction")
	context9789Prompt(t, h, "third")
	if len(seen) < 2 || seen[0].(agent.AgentMessage).Role() != "system" || seen[1].(agent.AgentMessage).Role() != "compactionSummary" {
		t.Fatalf("restored context=%+v", seen)
	}
	active := h.session.ActiveToolNames()
	if !slices.Contains(active, "bash") {
		t.Fatalf("active tools=%v", active)
	}
	want := slices.DeleteFunc(slices.Clone(active), func(name string) bool { return name == "bash" })
	if got := context9789ToolNames(request(t)); !slices.Equal(got, want) {
		t.Errorf("tools=%v want=%v", got, want)
	}
}

// Ports packages/coding-agent/test/suite/regressions/9789-context-handler-system-messages.test.ts:202.
func TestUpstream9789RemovedHeadReportsErrorButHonorsOutput(t *testing.T) {
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"context_with_system": {func(args ...any) (any, error) {
		messages := args[0].(extension.ContextWithSystemEvent).Messages
		return &extension.ContextEventResult{Messages: slices.DeleteFunc(slices.Clone(messages), func(m extension.AgentMessage) bool { return m.(agent.AgentMessage).System != nil })}, nil
	}}}}
	h := context9789Harness(t, ext)
	var errors []string
	h.session.currentRunner().AddErrorListener(func(err *extension.ExtensionError) { errors = append(errors, err.Event+": "+err.Error) })
	request := context9789Capture(h, "done")
	context9789Prompt(t, h, "hello")
	if got := context9789Roles(request(t)); !slices.Equal(got, []string{"user"}) {
		t.Errorf("roles=%v", got)
	}
	if len(errors) != 1 || !strings.HasPrefix(errors[0], "context_with_system: Handler removed the leading system message") {
		t.Errorf("errors=%v", errors)
	}
}
