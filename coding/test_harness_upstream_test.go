package coding

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/test/test-harness.test.ts describe "test harness"
// The upstream file tests createHarness: a faux stream function over declarative responses and a wired AgentSession. The Go counterpart is recoveryHarness over scriptedProvider and the real faux provider, so each case also exercises Session, retry, tool execution, event delivery and persistence.

func assistantMessages(h *recoveryHarness) []*agent.AssistantMessage {
	var out []*agent.AssistantMessage
	for _, message := range h.session.Messages() {
		if message.Assistant != nil {
			out = append(out, message.Assistant)
		}
	}
	return out
}

func firstText(message *agent.AssistantMessage) string {
	for _, block := range message.Content {
		if text, ok := block.(ai.TextContent); ok {
			return text.Text
		}
	}
	return ""
}

// fauxToolCalls is a response whose only content is one tool call, with the stop reason upstream derives for tool calls.
func fauxToolCalls(name string, arguments ai.JsonObject) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: time.Now().UnixMilli(),
			Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "faux_tc_" + name, Name: name, Arguments: arguments}}}
	}
}

func promptHarness(t *testing.T, h *recoveryHarness, text string) {
	t.Helper()
	if _, err := h.session.Prompt(t.Context(), text, nil); err != nil {
		t.Fatal(err)
	}
}

func updateEventTypes(log *eventLog) []ai.AssistantEventType {
	var out []ai.AssistantEventType
	for _, update := range eventsOf[agent.MessageUpdateEvent](log) {
		out = append(out, update.AssistantMessageEvent.EventType())
	}
	return out
}

func joinedDeltas(t *testing.T, log *eventLog, kind ai.AssistantEventType) (string, int) {
	t.Helper()
	var text strings.Builder
	count := 0
	for _, update := range eventsOf[agent.MessageUpdateEvent](log) {
		if update.AssistantMessageEvent.EventType() != kind {
			continue
		}
		count++
		switch event := update.AssistantMessageEvent.(type) {
		case ai.TextDeltaEvent:
			text.WriteString(event.Delta)
		case ai.ThinkingDeltaEvent:
			text.WriteString(event.Delta)
		}
	}
	return text.String(), count
}

func TestUpstreamTestHarness(t *testing.T) {
	t.Run("simple text response", func(t *testing.T) { // :19
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxReply("hello world", ai.StopReasonStop, 0))
		promptHarness(t, h, "hi")
		if h.provider.callCount() != 1 {
			t.Errorf("call count = %d", h.provider.callCount())
		}
		messages := assistantMessages(h)
		if len(messages) != 1 || len(messages[0].Content) != 1 || messages[0].Content[0] != (ai.TextContent{Text: "hello world"}) || messages[0].StopReason != ai.StopReasonStop {
			t.Fatalf("assistant messages = %+v", messages)
		}
	})

	t.Run("response sequence", func(t *testing.T) { // :34
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxReply("first", ai.StopReasonStop, 0), fauxReply("second", ai.StopReasonStop, 0), fauxReply("third", ai.StopReasonStop, 0))
		for _, prompt := range []string{"a", "b", "c"} {
			promptHarness(t, h, prompt)
		}
		var texts []string
		for _, message := range assistantMessages(h) {
			texts = append(texts, firstText(message))
		}
		if h.provider.callCount() != 3 || !slices.Equal(texts, []string{"first", "second", "third"}) {
			t.Errorf("calls = %d, texts = %v", h.provider.callCount(), texts)
		}
	})

	t.Run("tool call response triggers tool execution", func(t *testing.T) { // :50
		echo := &retryEchoTool{}
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, []agent.AgentTool{echo}, fauxToolCalls("echo", ai.JsonObject{"text": "hi"}), fauxReply("done after tool", ai.StopReasonStop, 0))
		promptHarness(t, h, "use the tool")
		if !slices.Equal(echo.ran(), []string{"hi"}) || h.provider.callCount() != 2 {
			t.Errorf("tool runs = %v, calls = %d", echo.ran(), h.provider.callCount())
		}
		results := 0
		for _, message := range h.session.Messages() {
			if message.ToolResult != nil {
				results++
			}
		}
		if results != 1 {
			t.Errorf("tool results = %d", results)
		}
	})

	t.Run("error response", func(t *testing.T) { // :78
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxError("something broke"))
		promptHarness(t, h, "hi")
		messages := assistantMessages(h)
		if len(messages) != 1 || messages[0].StopReason != ai.StopReasonError || messages[0].ErrorMessage != "something broke" {
			t.Fatalf("assistant messages = %+v", messages)
		}
	})

	t.Run("turns a pending terminal response into an error", func(t *testing.T) { // :91
		h := newRetryEventsHarness(t, `{"retry":{"enabled":false}}`, extension.Extension{}, nil, fauxReply("partial", ai.StopReasonPending, 0))
		promptHarness(t, h, "hi")
		messages := assistantMessages(h)
		if len(messages) != 1 || messages[0].StopReason != ai.StopReasonError || messages[0].ErrorMessage != "Faux response ended without a stop reason" {
			t.Fatalf("assistant messages = %+v", messages)
		}
	})

	t.Run("retry on transient error", func(t *testing.T) { // :105
		h := newRetryEventsHarness(t, retryEventsSettings, extension.Extension{}, nil, fauxError("overloaded_error"), fauxReply("recovered", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		promptHarness(t, h, "hi")
		starts, ends := eventsOf[agent.AutoRetryStartEvent](log), eventsOf[agent.AutoRetryEndEvent](log)
		if h.provider.callCount() != 2 || len(starts) != 1 || len(ends) != 1 || !ends[0].Success {
			t.Errorf("calls = %d, retry starts = %d, ends = %+v", h.provider.callCount(), len(starts), ends)
		}
	})

	t.Run("custom usage numbers", func(t *testing.T) { // :123
		h := newBoundaryHarness(t, harnessOptions{}, func([]ai.Message) *ai.AssistantMessage {
			return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "big response"}}, Provider: "faux", Model: "faux-1",
				Usage: ai.Usage{Input: 100000, Output: 5000}, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
		})
		promptHarness(t, h, "hi")
		messages := assistantMessages(h)
		if len(messages) != 1 || messages[0].Usage == nil || messages[0].Usage.Input != 100000 || messages[0].Usage.Output != 5000 {
			t.Fatalf("assistant messages = %+v", messages)
		}
	})

	t.Run("event capture", func(t *testing.T) { // :135
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxReply("hello", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		promptHarness(t, h, "hi")
		if starts, ends, messageEnds := len(eventsOf[agent.AgentStartEvent](log)), len(eventsOf[agent.AgentEndEvent](log)), len(eventsOf[agent.MessageEndEvent](log)); starts != 1 || ends != 1 || messageEnds < 2 {
			t.Errorf("agent_start = %d, agent_end = %d, message_end = %d (user and assistant)", starts, ends, messageEnds)
		}
	})

	t.Run("context capture", func(t *testing.T) { // :150
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxReply("reply", ai.StopReasonStop, 0))
		promptHarness(t, h, "my question")
		h.provider.mu.Lock()
		requests := slices.Clone(h.provider.requests)
		h.provider.mu.Unlock()
		if len(requests) != 1 || !strings.Contains(requests[0], `"role":"user"`) || !strings.Contains(requests[0], "my question") {
			t.Fatalf("captured contexts = %q", requests)
		}
	})

	t.Run("wraps around when more calls than responses", func(t *testing.T) { // :161
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxReply("a", ai.StopReasonStop, 0), fauxReply("b", ai.StopReasonStop, 0))
		h.provider.wrap = true
		for _, prompt := range []string{"1", "2", "3"} {
			promptHarness(t, h, prompt)
		}
		var texts []string
		for _, message := range assistantMessages(h) {
			texts = append(texts, firstText(message))
		}
		if h.provider.callCount() != 3 || !slices.Equal(texts, []string{"a", "b", "a"}) {
			t.Errorf("calls = %d, texts = %v", h.provider.callCount(), texts)
		}
	})

	t.Run("streams text deltas", func(t *testing.T) { // :177
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxReply("hello world", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		promptHarness(t, h, "hi")
		if text, count := joinedDeltas(t, log, ai.EventTextDelta); count == 0 || text != "hello world" {
			t.Errorf("text deltas = %d reconstructing %q", count, text)
		}
	})

	t.Run("streams thinking deltas", func(t *testing.T) { // :191
		thinking := func([]ai.Message) *ai.AssistantMessage {
			return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli(),
				Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "let me think about this"}, ai.TextContent{Text: "answer"}}}
		}
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, thinking)
		log := recordSessionEvents(h.session)
		promptHarness(t, h, "hi")
		types := updateEventTypes(log)
		count := func(kind ai.AssistantEventType) int {
			return len(slices.DeleteFunc(slices.Clone(types), func(t ai.AssistantEventType) bool { return t != kind }))
		}
		text, deltas := joinedDeltas(t, log, ai.EventThinkingDelta)
		if count(ai.EventThinkingStart) != 1 || deltas == 0 || count(ai.EventThinkingEnd) != 1 || text != "let me think about this" {
			t.Errorf("thinking starts = %d, deltas = %d (%q), ends = %d", count(ai.EventThinkingStart), deltas, text, count(ai.EventThinkingEnd))
		}
	})

	t.Run("streams tool call deltas", func(t *testing.T) { // :211
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, []agent.AgentTool{&retryEchoTool{}}, fauxToolCalls("echo", ai.JsonObject{"text": "hi"}), fauxReply("done", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		promptHarness(t, h, "use tool")
		types := updateEventTypes(log)
		count := func(kind ai.AssistantEventType) int {
			return len(slices.DeleteFunc(slices.Clone(types), func(t ai.AssistantEventType) bool { return t != kind }))
		}
		if count(ai.EventToolCallStart) != 1 || count(ai.EventToolCallDelta) == 0 || count(ai.EventToolCallEnd) != 1 {
			t.Errorf("tool call starts = %d, deltas = %d, ends = %d", count(ai.EventToolCallStart), count(ai.EventToolCallDelta), count(ai.EventToolCallEnd))
		}
	})

	t.Run("streams thinking then text then tool call in order", func(t *testing.T) { // :238
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, []agent.AgentTool{&retryEchoTool{}},
			fauxThinkingTextToolCall("hmm", "I will call a tool", "echo", ai.JsonObject{"text": "x"}), fauxReply("final", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		promptHarness(t, h, "do it")
		types := updateEventTypes(log)
		thinking, text, tool := slices.Index(types, ai.EventThinkingStart), slices.Index(types, ai.EventTextStart), slices.Index(types, ai.EventToolCallStart)
		if thinking < 0 || text < 0 || tool < 0 || thinking >= text || text >= tool {
			t.Errorf("first thinking_start = %d, text_start = %d, toolcall_start = %d", thinking, text, tool)
		}
	})

	t.Run("loads inline extension factories and disambiguates duplicate commands", func(t *testing.T) { // :274
		var mu sync.Mutex
		var calls []string
		command := func(path, label, description string) extension.Extension {
			return extension.Extension{Path: path, ResolvedPath: path, Commands: map[string]extension.RegisteredCommand{"shared-cmd": {
				Name: "shared-cmd", Description: description,
				Handler: func(_ context.Context, args string) error {
					mu.Lock()
					defer mu.Unlock()
					calls = append(calls, label+":"+args)
					return nil
				},
			}}}
		}
		h := newRecoveryHarness(t, harnessOptions{extensions: []extension.Extension{command("<alpha>", "alpha", "Alpha command"), command("<beta>", "beta", "Beta command")}})
		runner := h.session.ExtensionRunner()
		if runner == nil {
			t.Fatal("session has no extension runner")
		}
		type listed struct{ name, invocation, description, path string }
		var got []listed
		for _, c := range runner.Commands() {
			source, _ := c.SourceInfo.(map[string]any)
			path, _ := source["path"].(string)
			got = append(got, listed{c.Name, c.InvocationName, c.Description, path})
		}
		want := []listed{{"shared-cmd", "shared-cmd:1", "Alpha command", "<alpha>"}, {"shared-cmd", "shared-cmd:2", "Beta command", "<beta>"}}
		if !slices.Equal(got, want) {
			t.Fatalf("commands = %+v, want %+v", got, want)
		}
		for _, invocation := range [][2]string{{"shared-cmd:1", "first"}, {"shared-cmd:2", "second"}} {
			resolved, ok := runner.Command(invocation[0])
			if !ok {
				t.Fatalf("command %q missing", invocation[0])
			}
			if err := resolved.Handler(t.Context(), invocation[1]); err != nil {
				t.Fatal(err)
			}
		}
		if !slices.Equal(calls, []string{"alpha:first", "beta:second"}) {
			t.Errorf("calls = %v", calls)
		}
	})

	t.Run("session persistence works", func(t *testing.T) { // :326
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxReply("persisted", ai.StopReasonStop, 0))
		promptHarness(t, h, "hi")
		if entries := h.entries("message"); len(entries) < 2 {
			t.Errorf("message entries = %d, want user and assistant", len(entries))
		}
	})
}
