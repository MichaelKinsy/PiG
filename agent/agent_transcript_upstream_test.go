package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// .upstream/v0.87.1/packages/agent/test/agent.test.ts:158
func TestAgent_ConvertsInitialPromptAndToolsIntoTranscriptState(t *testing.T) {
	tool := &scriptTool{name: "echo", label: "Echo", description: "Echo input", params: map[string]any{"type": "object", "properties": map[string]any{}}}
	a := mustNewAgent(AgentOptions{SystemPrompt: "You are helpful.", Tools: []AgentTool{tool}})
	initial := a.Messages()[0].System
	if initial == nil || initial.Content != ai.SystemText("You are helpful.") || len(initial.ToolsAdded) != 1 || initial.ToolsAdded[0].Name != "echo" {
		t.Fatalf("initial = %+v", initial)
	}
}

// .upstream/v0.87.1/packages/agent/test/agent.test.ts:178
func TestAgent_DeclaresToolLoadoutChangesBeforeNextRequest(t *testing.T) {
	first, second := &scriptTool{name: "first", params: map[string]any{"type": "object", "properties": map[string]any{}}}, &scriptTool{name: "second", params: map[string]any{"type": "object", "properties": map[string]any{}}}
	var requests [][]string
	p := &scriptedProvider{respond: func(_ int, req scriptedRequest) *ai.AssistantMessageEventStream {
		var changes []string
		for _, m := range req.transcript.Messages() {
			if s, ok := m.(ai.SystemMessage); ok {
				var added, removed []string
				for _, tool := range s.ToolsAdded {
					added = append(added, tool.Name)
				}
				for _, tool := range s.ToolsRemoved {
					removed = append(removed, tool.Name)
				}
				changes = append(changes, "+"+strings.Join(added, ","), "-"+strings.Join(removed, ","))
			}
		}
		requests = append(requests, changes)
		return doneStream(textMessage("done"))
	}}
	a := mustNewAgent(AgentOptions{SystemPrompt: "You are helpful.", Tools: []AgentTool{first}, Model: scriptedModel(p)})
	mustSend(t, a, "one")
	a.SetTools([]AgentTool{second})
	mustSend(t, a, "two")
	mustSend(t, a, "three")
	want := [][]string{{"+first", "-"}, {"+first", "-", "+second", "-first"}, {"+first", "-", "+second", "-first"}}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	var update *ai.SystemMessage
	for _, m := range a.Messages() {
		if m.System != nil && len(m.System.ToolsRemoved) > 0 {
			update = m.System
			break
		}
	}
	if update == nil {
		t.Fatal("missing loadout delta")
	}
	expected := &ai.SystemMessage{Content: ai.SystemText(""), ToolsAdded: []ai.ToolSchema{second.Schema()}, ToolsRemoved: []ai.ToolReference{{Name: "first"}}, Timestamp: update.Timestamp}
	if !reflect.DeepEqual(update, expected) {
		t.Fatalf("update = %#v, want %#v", update, expected)
	}
	encoded, err := json.Marshal(a.Messages()[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "execute") {
		t.Fatalf("executable in declaration: %s", encoded)
	}
}

// .upstream/v0.87.1/packages/agent/test/agent.test.ts:226
func TestAgent_MergesToolChangesIntoPendingSystemMessage(t *testing.T) {
	tool := &scriptTool{name: "echo", label: "Echo", description: "Echo input", params: map[string]any{"type": "object", "properties": map[string]any{}}}
	p := &scriptedProvider{respond: func(_ int, req scriptedRequest) *ai.AssistantMessageEventStream {
		count := 0
		for _, m := range req.transcript.Messages() {
			if _, ok := m.(ai.SystemMessage); ok {
				count++
			}
		}
		if count != 2 {
			t.Errorf("system messages = %d, want 2", count)
		}
		return doneStream(textMessage("done"))
	}}
	a := mustNewAgent(AgentOptions{SystemPrompt: "You are helpful.", Model: scriptedModel(p)})
	a.SetTools([]AgentTool{tool})
	pending := &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "skills", Value: new("<skills>x</skills>")}}, Timestamp: 1}
	if _, err := a.SendMessages(t.Context(), []AgentMessage{{System: pending}, userMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	expected := &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "skills", Value: new("<skills>x</skills>")}}, ToolsAdded: []ai.ToolSchema{tool.Schema()}, Timestamp: 1}
	if !reflect.DeepEqual(a.Messages()[1].System, expected) {
		t.Fatalf("pending = %#v, want %#v", a.Messages()[1].System, expected)
	}
}

// .upstream/v0.87.1/packages/agent/test/agent.test.ts:261
func TestAgent_RewritesPendingToolDeclarationsToExecutableSet(t *testing.T) {
	first, second := &scriptTool{name: "first", params: map[string]any{"type": "object", "properties": map[string]any{}}}, &scriptTool{name: "second", params: map[string]any{"type": "object", "properties": map[string]any{}}}
	a := mustNewAgent(AgentOptions{SystemPrompt: "You are helpful.", Tools: []AgentTool{first}, Model: scriptedModel(&scriptedProvider{respond: replyText("done")})})
	pending := &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "note", Value: new("<note>x</note>")}}, ToolsAdded: []ai.ToolSchema{second.Schema()}, ToolsRemoved: []ai.ToolReference{{Name: "first"}}, Timestamp: 1}
	if _, err := a.SendMessages(t.Context(), []AgentMessage{{System: pending}, userMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	expected := &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "note", Value: new("<note>x</note>")}}, Timestamp: 1}
	if !reflect.DeepEqual(a.Messages()[1].System, expected) {
		t.Fatalf("pending = %#v, want %#v", a.Messages()[1].System, expected)
	}
	current := ai.GetCurrentSystemMessage(systemMessages(a.Messages()))
	if current == nil || len(current.ToolsAdded) != 1 || current.ToolsAdded[0].Name != "first" {
		t.Fatalf("current = %+v", current)
	}
}

// .upstream/v0.87.1/packages/agent/test/agent.test.ts:296
func TestAgent_RestoresTranscriptBaselineWhenReset(t *testing.T) {
	tool := &scriptTool{name: "echo", label: "Echo", description: "Echo input", params: map[string]any{"type": "object", "properties": map[string]any{}}}
	a := mustNewAgent(AgentOptions{SystemPrompt: "You are helpful.", Tools: []AgentTool{tool}})
	a.SetMessages(append(slices.Clone(a.Messages()), userMessage("old")))
	if err := a.Reset(); err != nil {
		t.Fatal(err)
	}
	if len(a.Messages()) != 1 {
		t.Fatalf("messages = %v", a.Messages())
	}
	initial := a.Messages()[0].System
	if initial == nil || initial.Content != ai.SystemText("You are helpful.") || len(initial.ToolsAdded) != 1 || initial.ToolsAdded[0].Name != "echo" {
		t.Fatalf("initial = %+v", initial)
	}
}

// .upstream/v0.87.1/packages/agent/test/agent.test.ts:376
// A blocking callback is the Go equivalent of an awaited Promise. In
// particular, receiving agent_end on EventCh alone does not await its consumer.
func TestAgent_AwaitsAgentEndSubscriberBeforePromptResolves(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		barrier := make(chan struct{})
		finished, resolved := false, false
		a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("ok")})})
		a.Subscribe(func(_ context.Context, ev AgentEvent) error {
			if _, ok := ev.(AgentEndEvent); ok {
				<-barrier
				finished = true
			}
			return nil
		})
		go func() { mustSend(t, a, "hello"); resolved = true }()
		synctest.Wait()
		if resolved || finished || !a.IsStreaming() {
			t.Fatalf("before barrier: resolved=%v finished=%v streaming=%v", resolved, finished, a.IsStreaming())
		}
		close(barrier)
		synctest.Wait()
		if !resolved || !finished || a.IsStreaming() {
			t.Fatalf("after barrier: resolved=%v finished=%v streaming=%v", resolved, finished, a.IsStreaming())
		}
	})
}

// .upstream/v0.87.1/packages/agent/test/agent-loop.test.ts:167
func TestAgentLoop_BuildsProviderContextExclusivelyFromTranscriptMessages(t *testing.T) {
	initial := &ai.SystemMessage{Content: ai.SystemText("Transcript prompt"), ToolsAdded: []ai.ToolSchema{}, Timestamp: 1}
	called := false
	a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("unused")}), StreamFn: func(_ context.Context, _ *ai.Model, transcript ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		called = true
		// TranscriptContext exposes Messages through an accessor in Go. Inspect
		// its data fields, as Object.keys does in Pi; it is not a JSON wire DTO.
		// Go retains normalization errors until the provider validates the
		// request; that error slot is not provider context data.
		var fields []string
		for field := range reflect.TypeFor[ai.TranscriptContext]().Fields() {
			if field.Type != reflect.TypeFor[error]() {
				fields = append(fields, field.Name)
			}
		}
		if !slices.Equal(fields, []string{"messages"}) {
			t.Fatalf("provider context data fields = %v", fields)
		}
		messages := transcript.Messages()
		if len(messages) != 2 {
			t.Fatalf("messages = %v", messages)
		}
		system, ok := messages[0].(ai.SystemMessage)
		if !ok || !reflect.DeepEqual(system, *initial) {
			t.Fatalf("initial system = %#v, want %#v", messages[0], *initial)
		}
		return doneStream(textMessage("done")), nil
	}})
	if _, err := a.SendMessages(t.Context(), []AgentMessage{{System: initial}, userMessage("Hello")}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("provider was not called")
	}
}
