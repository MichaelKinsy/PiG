package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type emitProbeStep struct {
	Type                  string          `json:"type"`
	Messages              []string        `json:"messages"`
	Message               string          `json:"message,omitempty"`
	ToolResults           []string        `json:"toolResults"`
	PreDispatched         bool            `json:"preDispatched,omitempty"`
	TurnIndex             int             `json:"turnIndex"`
	Replacement           json.RawMessage `json:"replacement,omitempty"`
	ReplaceWithSelf       bool            `json:"replaceWithSelf,omitempty"`
	AssistantMessageEvent json.RawMessage `json:"assistantMessageEvent,omitempty"`
	AssistantEventName    string          `json:"-"`
	ToolCallID            string          `json:"toolCallId,omitempty"`
	ToolName              string          `json:"toolName,omitempty"`
	Args                  json.RawMessage `json:"args,omitempty"`
	PartialResult         json.RawMessage `json:"partialResult,omitempty"`
	Result                json.RawMessage `json:"result,omitempty"`
	ResultName            string          `json:"-"`
	IsError               bool            `json:"isError"`
	DurationMs            *int64          `json:"durationMs,omitempty"`
}

type emitProbe struct {
	Name     string                     `json:"-"`
	Messages map[string]json.RawMessage `json:"messages"`
	Steps    []emitProbeStep            `json:"steps"`
}

type emitRecord struct {
	Kind        string `json:"kind"`
	Event       string `json:"event,omitempty"`
	TurnIndex   int    `json:"turnIndex,omitempty"`
	Message     string `json:"message,omitempty"`
	ToolResults string `json:"toolResults"`
}

type emitStepResult struct {
	Record    []emitRecord `json:"record"`
	Message   *string      `json:"message"`
	TurnIndex int          `json:"turnIndex"`
}

const (
	emitUser      = `{"role":"user","content":[{"type":"text","text":"hi"}],"timestamp":1000}`
	emitAssistant = `{"role":"assistant","content":[{"type":"text","text":"hello"}],"api":"faux","provider":"faux","model":"faux-1","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1001}`
	emitToolCall  = `{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"read","arguments":{"path":"a.txt"}}],"api":"faux","provider":"faux","model":"faux-1","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1002}`
	emitToolRes   = `{"role":"toolResult","toolCallId":"call-1","toolName":"read","content":[{"type":"text","text":"file text"}],"isError":false,"timestamp":1003}`
	emitCustom    = `{"role":"custom","customType":"note","content":"custom text","display":true,"timestamp":1004}`
	emitSystem    = `{"role":"system","content":"system text","timestamp":1005}`
)

// emitProbeToolResults are the tool results the probes name, as the JSON Pi's runner receives and the value the Pig agent event carries.
var emitProbeToolResults = map[string]struct {
	json string
	pig  agent.AgentToolResult
}{
	"text":    {`{"content":[{"type":"text","text":"ok"}]}`, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}},
	"details": {`{"content":[{"type":"text","text":"ok"}],"details":{"lines":3,"nested":{"a":[1,2]}}}`, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: map[string]any{"lines": float64(3), "nested": map[string]any{"a": []any{float64(1), float64(2)}}}}},
	"empty":   {`{"content":[]}`, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}}},
	"two":     {`{"content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}`, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "one"}, ai.TextContent{Text: "two"}}}},
}

func emitProbeMessages() map[string]json.RawMessage {
	return map[string]json.RawMessage{"u": json.RawMessage(emitUser), "a": json.RawMessage(emitAssistant), "a2": json.RawMessage(emitToolCall), "t": json.RawMessage(emitToolRes), "c": json.RawMessage(emitCustom), "s": json.RawMessage(emitSystem)}
}

func emitProbes() []emitProbe {
	i64 := func(v int64) *int64 { return &v }
	textDelta := `{"type":"text_delta","contentIndex":0,"delta":"hel","partial":` + emitAssistant + `}`
	start := `{"type":"start","partial":` + emitAssistant + `}`
	done := `{"type":"done","reason":"stop","message":` + emitAssistant + `}`
	step := func(kind string) emitProbeStep { return emitProbeStep{Type: kind} }
	end := func(message, replacement string) emitProbeStep {
		s := emitProbeStep{Type: "message_end", Message: message}
		if replacement != "" {
			s.Replacement = json.RawMessage(replacement)
		}
		return s
	}
	with := func(s emitProbeStep, edit func(*emitProbeStep)) emitProbeStep { edit(&s); return s }

	probes := []emitProbe{{
		Name: "two runs of tool turns: index per run, boundary once per turn",
		Steps: []emitProbeStep{
			step("agent_start"),
			{Type: "turn_start", TurnIndex: 0},
			{Type: "message_start", Message: "u"}, end("u", ""),
			{Type: "message_start", Message: "a2"},
			{Type: "message_update", Message: "a2", AssistantMessageEvent: json.RawMessage(start), AssistantEventName: "start"},
			end("a2", ""),
			{Type: "tool_execution_start", ToolCallID: "call-1", ToolName: "read", Args: json.RawMessage(`{"path":"a.txt"}`)},
			{Type: "tool_execution_update", ToolCallID: "call-1", ToolName: "read", Args: json.RawMessage(`{"path":"a.txt"}`), PartialResult: json.RawMessage(emitProbeToolResults["text"].json), ResultName: "text"},
			{Type: "tool_execution_end", ToolCallID: "call-1", ToolName: "read", Result: json.RawMessage(emitProbeToolResults["details"].json), ResultName: "details", DurationMs: i64(12)},
			{Type: "message_start", Message: "t"}, end("t", ""),
			{Type: "turn_end", Message: "a2", ToolResults: []string{"t"}},
			{Type: "turn_start", TurnIndex: 1},
			{Type: "message_start", Message: "a"},
			{Type: "message_update", Message: "a", AssistantMessageEvent: json.RawMessage(textDelta), AssistantEventName: "text_delta"},
			{Type: "message_update", Message: "a", AssistantMessageEvent: json.RawMessage(done), AssistantEventName: "done"},
			end("a", ""),
			{Type: "turn_end", Message: "a", ToolResults: []string{}},
			{Type: "agent_end", Messages: []string{"u", "a2", "t", "a"}},
			step("agent_start"),
			{Type: "turn_start", TurnIndex: 0},
			end("a", ""),
			{Type: "turn_end", Message: "a", ToolResults: []string{}},
			{Type: "agent_end", Messages: []string{}},
		},
	}, {
		Name: "a message the finish-turn hook already dispatched is not dispatched again, once",
		Steps: []emitProbeStep{
			step("agent_start"),
			{Type: "turn_start", TurnIndex: 0},
			end("a", ""),
			{Type: "turn_end", Message: "a", ToolResults: []string{}, PreDispatched: true},
			{Type: "turn_start", TurnIndex: 1},
			{Type: "turn_end", Message: "a", ToolResults: []string{}},
			{Type: "turn_end", Message: "a", ToolResults: []string{}, PreDispatched: true},
			{Type: "turn_end", Message: "a", ToolResults: []string{}},
		},
	}, {
		Name: "a turn_end whose message is not an assistant message dispatches nothing and still counts the turn",
		Steps: []emitProbeStep{
			step("agent_start"),
			end("u", ""),
			{Type: "turn_end", Message: "u", ToolResults: []string{}},
			{Type: "turn_end", Message: "c", ToolResults: []string{}},
			end("a", ""),
			{Type: "turn_end", Message: "a", ToolResults: []string{}},
			{Type: "turn_start", TurnIndex: 3},
		},
	}, {
		Name: "tool_execution_end reports durationMs only when the event has one",
		Steps: []emitProbeStep{
			{Type: "tool_execution_end", ToolCallID: "c1", ToolName: "bash", Result: json.RawMessage(emitProbeToolResults["text"].json), ResultName: "text"},
			{Type: "tool_execution_end", ToolCallID: "c2", ToolName: "bash", Result: json.RawMessage(emitProbeToolResults["empty"].json), ResultName: "empty", DurationMs: i64(0)},
			{Type: "tool_execution_end", ToolCallID: "c3", ToolName: "bash", Result: json.RawMessage(emitProbeToolResults["two"].json), ResultName: "two", IsError: true, DurationMs: i64(86_400_000)},
			{Type: "tool_execution_start", ToolCallID: "c4", ToolName: "bash", Args: json.RawMessage(`{}`)},
			{Type: "tool_execution_start", ToolCallID: "c5", ToolName: "bash", Args: json.RawMessage(`{"z":1,"a":{"y":[1,"two",null,true],"b":2}}`)},
			{Type: "tool_execution_start", ToolCallID: "c6", ToolName: "bash", Args: json.RawMessage(`[1,2]`)},
			{Type: "tool_execution_start", ToolCallID: "c7", ToolName: "bash", Args: json.RawMessage(`"text"`)},
			{Type: "tool_execution_update", ToolCallID: "c5", ToolName: "bash", Args: json.RawMessage(`{"z":1,"a":{"y":[1,"two",null,true],"b":2}}`), PartialResult: json.RawMessage(emitProbeToolResults["details"].json), ResultName: "details"},
		},
	}, {
		Name: "message_start, message_update and agent_end for every role",
		Steps: []emitProbeStep{
			{Type: "message_start", Message: "u"}, {Type: "message_start", Message: "a"}, {Type: "message_start", Message: "t"}, {Type: "message_start", Message: "c"}, {Type: "message_start", Message: "s"},
			{Type: "message_update", Message: "a", AssistantMessageEvent: json.RawMessage(textDelta), AssistantEventName: "text_delta"},
			{Type: "message_update", Message: "a", AssistantMessageEvent: json.RawMessage(start), AssistantEventName: "start"},
			{Type: "agent_end", Messages: []string{"u", "a", "t", "c", "s"}},
		},
	}}

	// message_end replacements: the same role with other content, content that is missing or null (the four roles that get an empty content list and the system role
	// that does not), a replacement that adds members, and a handler that returns the event's own message.
	replacements := []struct {
		name, message, replacement string
		self                       bool
	}{
		{"user text", "u", `{"role":"user","content":[{"type":"text","text":"redacted"}],"timestamp":1000}`, false},
		{"user string content", "u", `{"role":"user","content":"plain string","timestamp":1000}`, false},
		{"user null content", "u", `{"role":"user","content":null,"timestamp":1000}`, false},
		{"user missing content", "u", `{"role":"user","timestamp":1000}`, false},
		{"assistant text and usage", "a", `{"role":"assistant","content":[{"type":"text","text":"[redacted]"}],"api":"faux","provider":"faux","model":"faux-1","usage":{"input":37,"output":13,"cacheRead":11,"cacheWrite":7,"totalTokens":68,"cost":{"input":0.125,"output":0.25,"cacheRead":0.0625,"cacheWrite":0.0625,"total":0.5}},"stopReason":"stop","timestamp":1001}`, false},
		{"assistant null content", "a", `{"role":"assistant","content":null,"api":"faux","provider":"faux","model":"faux-1","stopReason":"stop","timestamp":1001}`, false},
		{"assistant missing content", "a", `{"role":"assistant","api":"faux","provider":"faux","model":"faux-1","stopReason":"stop","timestamp":1001}`, false},
		{"toolResult null content", "t", `{"role":"toolResult","toolCallId":"call-1","toolName":"read","content":null,"isError":false,"timestamp":1003}`, false},
		{"toolResult new text", "t", `{"role":"toolResult","toolCallId":"call-1","toolName":"read","content":[{"type":"text","text":"new"}],"isError":true,"timestamp":1003}`, false},
		{"custom null content", "c", `{"role":"custom","customType":"note","content":null,"display":true,"timestamp":1004}`, false},
		{"custom blocks", "c", `{"role":"custom","customType":"note","content":[{"type":"text","text":"blocks"}],"display":false,"details":{"k":1},"timestamp":1004}`, false},
		{"system null content", "s", `{"role":"system","content":null,"timestamp":1005}`, false},
		{"system new text", "s", `{"role":"system","content":"replaced system","timestamp":1005}`, false},
		{"the event's own message", "a", "", true},
	}
	for _, r := range replacements {
		s := end(r.message, r.replacement)
		s.ReplaceWithSelf = r.self
		probes = append(probes, emitProbe{Name: "message_end replacement: " + r.name, Steps: []emitProbeStep{
			{Type: "message_start", Message: r.message}, s, end("a", ""), with(step("turn_end"), func(s *emitProbeStep) { s.Message, s.ToolResults = "a", []string{} }),
		}})
	}
	for i := range probes {
		probes[i].Messages = emitProbeMessages()
	}
	return probes
}

var emitTurnStartTimestamp = regexp.MustCompile(`,"timestamp":\d+\}$`)

// runEmitProbeWithPig feeds the probe's events through Session.handleAgentEvent with one extension that records every event it receives in the JSON a handler would see.
func runEmitProbeWithPig(t *testing.T, probe emitProbe) []emitStepResult {
	t.Helper()
	messages := map[string]agent.AgentMessage{}
	for name, raw := range probe.Messages {
		var message agent.AgentMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatalf("message %s: %v", name, err)
		}
		messages[name] = message
	}
	var mu sync.Mutex
	var records []emitRecord
	var replacement *agent.AgentMessage
	record := func(kind string, event any) {
		data, err := json.Marshal(event)
		if err != nil {
			t.Errorf("marshal %T: %v", event, err)
		}
		mu.Lock()
		records = append(records, emitRecord{Kind: kind, Event: string(data)})
		mu.Unlock()
	}
	handlers := map[string][]extension.HandlerFn{}
	for _, name := range []string{"agent_start", "agent_end", "turn_start", "message_start", "message_update", "tool_execution_start", "tool_execution_update", "tool_execution_end"} {
		handlers[name] = []extension.HandlerFn{func(args ...any) (any, error) { record("emit", args[0]); return nil, nil }}
	}
	handlers["message_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
		record("emitMessageEnd", args[0])
		if replacement == nil {
			return nil, nil
		}
		out := extension.AgentMessage(*replacement)
		return &extension.MessageEndEventResult{Message: &out}, nil
	}}
	var session *Session
	handlers["turn_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
		event := args[0].(extension.TurnEndEvent)
		message, _ := json.Marshal(event.Message)
		results, _ := json.Marshal(event.ToolResults)
		mu.Lock()
		records = append(records, emitRecord{Kind: "boundary", TurnIndex: event.TurnIndex, Message: string(message), ToolResults: string(results)})
		mu.Unlock()
		return nil, nil
	}}
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/recorder", Handlers: handlers}})
	session = h.session

	results := make([]emitStepResult, 0, len(probe.Steps))
	for index, step := range probe.Steps {
		mu.Lock()
		records = nil
		mu.Unlock()
		replacement = nil
		if len(step.Replacement) > 0 {
			var message agent.AgentMessage
			if err := json.Unmarshal(step.Replacement, &message); err != nil {
				t.Fatalf("step %d replacement: %v", index, err)
			}
			replacement = &message
		}
		var event agent.AgentEvent
		var subject *agent.AgentMessage
		pick := func(name string) agent.AgentMessage { return messages[name] }
		switch step.Type {
		case "agent_start":
			event = agent.AgentStartEvent{}
		case "agent_end":
			list := make([]agent.AgentMessage, len(step.Messages))
			for i, name := range step.Messages {
				list[i] = pick(name)
			}
			event = agent.AgentEndEvent{Messages: list}
		case "turn_start":
			event = agent.TurnStartEvent{TurnIndex: step.TurnIndex}
		case "turn_end":
			message := pick(step.Message)
			subject = &message
			var toolResults []agent.ToolResultMessage
			for _, name := range step.ToolResults {
				toolResults = append(toolResults, *pick(name).ToolResult)
			}
			if step.PreDispatched && message.Assistant != nil {
				session.boundaryDispatchedMessage = message.Assistant
			}
			event = agent.TurnEndEvent{Message: message, ToolResults: toolResults}
		case "message_start":
			message := pick(step.Message)
			subject = &message
			event = agent.MessageStartEvent{Message: message}
		case "message_update":
			message := pick(step.Message)
			subject = &message
			var partial ai.AssistantMessage
			if err := json.Unmarshal([]byte(emitAssistant), &partial); err != nil {
				t.Fatal(err)
			}
			var update ai.AssistantMessageEvent
			switch step.AssistantEventName {
			case "start":
				update = ai.StartEvent{Partial: &partial}
			case "text_delta":
				update = ai.TextDeltaEvent{ContentIndex: 0, Delta: "hel", Partial: &partial}
			case "done":
				update = ai.DoneEvent{Reason: ai.StopReasonStop, Message: &partial}
			default:
				t.Fatalf("unknown assistant event %q", step.AssistantEventName)
			}
			event = agent.MessageUpdateEvent{Message: message, AssistantMessageEvent: update}
		case "message_end":
			message := pick(step.Message)
			subject = &message
			if step.ReplaceWithSelf {
				replacement = &message
			}
			event = agent.MessageEndEvent{Message: message}
		case "tool_execution_start":
			event = agent.ToolExecutionStartEvent{ToolCallID: step.ToolCallID, ToolName: step.ToolName, Args: step.Args}
		case "tool_execution_update":
			event = agent.ToolExecutionUpdateEvent{ToolCallID: step.ToolCallID, ToolName: step.ToolName, Args: step.Args, PartialResult: emitProbeToolResults[step.ResultName].pig}
		case "tool_execution_end":
			event = agent.ToolExecutionEndEvent{ToolCallID: step.ToolCallID, ToolName: step.ToolName, Result: emitProbeToolResults[step.ResultName].pig, IsError: step.IsError, DurationMs: step.DurationMs}
		default:
			t.Fatalf("unknown step %q", step.Type)
		}
		before := time.Now().UnixMilli()
		if err := session.handleAgentEvent(context.Background(), event); err != nil {
			t.Fatalf("step %d %s: %v", index, step.Type, err)
		}
		after := time.Now().UnixMilli()
		result := emitStepResult{TurnIndex: session.boundaryTurnIndex}
		mu.Lock()
		result.Record = slices.Clone(records)
		mu.Unlock()
		for i, rec := range result.Record {
			if rec.Kind == "emit" && strings.HasPrefix(rec.Event, `{"type":"turn_start"`) {
				stamp := emitTurnStartTimestamp.FindString(rec.Event)
				var ms int64
				if _, err := fmt.Sscanf(stamp, `,"timestamp":%d}`, &ms); err != nil || ms < before || ms > after {
					t.Errorf("step %d: turn_start timestamp %q is not the dispatch time in [%d, %d]", index, stamp, before, after)
				}
				result.Record[i].Event = strings.TrimSuffix(emitTurnStartTimestamp.ReplaceAllString(rec.Event, "}"), "")
			}
		}
		if subject != nil {
			data, err := json.Marshal(*subject)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			result.Message = &text
		}
		results = append(results, result)
	}
	return results
}

// AgentSession._emitExtensionEvent (agent-session.ts) against pinned Pi: every agent event reaches the extension runner as Pi's typed event (agent_start, agent_end with its
// messages, turn_start under the session's turn index, message_start/update/end, tool_execution_start/update/end with durationMs only when the event has one); a
// turn_end dispatches the boundary once per assistant message unless the finish-turn hook already did, and counts the turn either way; a message_end replacement is normalized
// (missing or null content becomes an empty list for the user, assistant, toolResult and custom roles, not for system) and replaces the event's message in place. Compared in
// the JSON an extension handler would stringify, member order included.
func TestEmitExtensionEventMatchesPi(t *testing.T) {
	probes := emitProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/emit_extension_event.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want [][]struct {
		emitStepResult
		ClockAdvanced int `json:"clockAdvanced"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(probes) {
		t.Fatalf("Pi answered %d probes, want %d", len(want), len(probes))
	}
	normalizePi := func(rec emitRecord) emitRecord {
		if rec.Kind == "emit" && strings.HasPrefix(rec.Event, `{"type":"turn_start"`) {
			rec.Event = emitTurnStartTimestamp.ReplaceAllString(rec.Event, "}")
		}
		return rec
	}
	boundaries, replacements := 0, 0
	for p, probe := range probes {
		t.Run(probe.Name, func(t *testing.T) {
			got := runEmitProbeWithPig(t, probe)
			for i, step := range got {
				expected := want[p][i]
				piRecord := make([]emitRecord, len(expected.Record))
				for k, rec := range expected.Record {
					piRecord[k] = normalizePi(rec)
					if rec.Kind == "boundary" {
						boundaries++
					}
					if rec.Kind == "emitMessageEnd" && probe.Steps[i].Replacement != nil {
						replacements++
					}
				}
				if !slices.Equal(step.Record, piRecord) || !equalOptionalString(step.Message, expected.Message) || step.TurnIndex != expected.TurnIndex {
					t.Errorf("step %d (%s):\n  Pig record=%s message=%s turnIndex=%d\n  Pi  record=%s message=%s turnIndex=%d", i, probe.Steps[i].Type, mustJSON(step.Record), optionalString(step.Message), step.TurnIndex, mustJSON(piRecord), optionalString(expected.Message), expected.TurnIndex)
				}
			}
		})
	}
	if boundaries == 0 || replacements == 0 {
		t.Fatalf("the probes dispatched %d boundaries and applied %d replacements; both must be exercised", boundaries, replacements)
	}
}

func equalOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func optionalString(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
