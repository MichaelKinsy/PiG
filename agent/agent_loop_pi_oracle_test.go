package agent

// pi: packages/agent/src/agent-loop.ts (runAgentLoop, runLoop, streamAssistantResponse, executeToolCalls, prepareToolCall, finalizeExecutedToolCall)

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type loopTool struct {
	name, mode string
	kind, text string
	updates    int
	terminate  bool
	abort      func()
}

func (t *loopTool) Name() string  { return t.name }
func (t *loopTool) Label() string { return t.name }

// Schema decodes the declaration so its parameters keep the Pi tool's key order: Pi's declarationsEqual compares JSON.stringify output, and a Go map literal marshals its keys sorted.
func (t *loopTool) Schema() ai.ToolSchema {
	var schema ai.ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"`+t.name+`","description":"`+t.name+`","parameters":`+loopToolParameters+`}`), &schema); err != nil {
		panic(err)
	}
	return schema
}

const loopToolParameters = `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`

func (t *loopTool) ExecutionMode() ToolExecutionMode { return ToolExecutionMode(t.mode) }
func (t *loopTool) Execute(_ context.Context, _ string, _ json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
	for i := range t.updates {
		onUpdate(AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("u%d", i)}}, Details: map[string]any{"i": i}})
	}
	if t.abort != nil {
		t.abort()
	}
	if t.kind == "throw" {
		return AgentToolResult{}, errors.New(t.text)
	}
	return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: t.text}}, Details: map[string]any{}, IsError: t.kind == "error", Terminate: t.terminate}, nil
}

type loopProgram struct {
	Tools []struct {
		Name     string `json:"name"`
		Mode     string `json:"mode,omitempty"`
		Behavior struct {
			Kind      string `json:"kind"`
			Text      string `json:"text"`
			Updates   int    `json:"updates"`
			Terminate bool   `json:"terminate"`
			Abort     bool   `json:"abort"`
		} `json:"behavior"`
	} `json:"tools"`
	Execution string `json:"execution,omitempty"`
	Turns     []struct {
		Content []map[string]any `json:"content"`
		Stop    string           `json:"stop"`
		Stream  string           `json:"stream"`
	} `json:"turns"`
	Steering  [][]map[string]any        `json:"steering"`
	FollowUps [][]map[string]any        `json:"followUps"`
	Finish    []*string                 `json:"finish"`
	Before    map[string]map[string]any `json:"before"`
	After     map[string]map[string]any `json:"after"`
	Prompts   []map[string]any          `json:"prompts"`
	Context   []map[string]any          `json:"context"`
	// AbortAt aborts the run's signal while the before hook of this tool call runs.
	AbortAt string `json:"abortAt"`
}

func abortFn(enabled bool, fn func()) func() {
	if enabled {
		return fn
	}
	return nil
}

func loopPrograms() []loopProgram {
	random := rand.New(rand.NewSource(20261029))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	user := func(text string) map[string]any {
		return map[string]any{"role": "user", "content": text, "timestamp": 5}
	}
	text := func() string { return pick("hi", "", "ünï", "a\nb") }
	declared := rand.New(rand.NewSource(20261030))
	declaration := func(name string) map[string]any {
		return map[string]any{"name": name, "description": name, "parameters": json.RawMessage(loopToolParameters)}
	}
	system := func() map[string]any {
		message := map[string]any{"role": "system", "content": []string{"", "rules"}[declared.Intn(2)], "timestamp": 5}
		names := []string{"a", "b", "c", "gone"}
		if declared.Intn(2) == 0 {
			added := []any{}
			for _, name := range names {
				if declared.Intn(2) == 0 {
					added = append(added, declaration(name))
				}
			}
			message["toolsAdded"] = added
		}
		if declared.Intn(2) == 0 {
			removed := []any{}
			for _, name := range names {
				if declared.Intn(3) == 0 {
					removed = append(removed, map[string]any{"name": name})
				}
			}
			message["toolsRemoved"] = removed
		}
		return message
	}
	programs := make([]loopProgram, 0, 3000)
	for range 3000 {
		var p loopProgram
		var ids []string
		// A tool that aborts the run races its parallel siblings: Pi starts them in order within one tick, goroutines do not. Only a sequential run is deterministic.
		sequential := random.Intn(3) == 0
		for _, name := range []string{"a", "b", "c"} {
			if random.Intn(5) == 0 {
				continue
			}
			var tool struct {
				Name     string `json:"name"`
				Mode     string `json:"mode,omitempty"`
				Behavior struct {
					Kind      string `json:"kind"`
					Text      string `json:"text"`
					Updates   int    `json:"updates"`
					Terminate bool   `json:"terminate"`
					Abort     bool   `json:"abort"`
				} `json:"behavior"`
			}
			tool.Name = name
			if random.Intn(4) == 0 {
				tool.Mode = pick("sequential", "parallel")
			}
			tool.Behavior.Kind = pick("ok", "ok", "error", "throw")
			tool.Behavior.Text = pick("r", "result text", "")
			tool.Behavior.Updates = random.Intn(3) / 2
			tool.Behavior.Terminate = random.Intn(3) == 0
			tool.Behavior.Abort = sequential && random.Intn(5) == 0
			p.Tools = append(p.Tools, tool)
		}
		if p.Tools == nil {
			p.Tools = make([]struct {
				Name     string `json:"name"`
				Mode     string `json:"mode,omitempty"`
				Behavior struct {
					Kind      string `json:"kind"`
					Text      string `json:"text"`
					Updates   int    `json:"updates"`
					Terminate bool   `json:"terminate"`
					Abort     bool   `json:"abort"`
				} `json:"behavior"`
			}, 0)
		}
		if sequential {
			p.Execution = "sequential"
		} else if random.Intn(2) == 0 {
			p.Execution = "parallel"
		}
		p.Before, p.After = map[string]map[string]any{}, map[string]map[string]any{}
		for turn := range random.Intn(6) {
			stop := pick("stop", "toolUse", "toolUse", "toolUse", "length", "error", "aborted")
			content := []map[string]any{}
			if random.Intn(2) == 0 {
				content = append(content, map[string]any{"type": "text", "text": text()})
			}
			if stop != "stop" || random.Intn(5) == 0 {
				for k := range 1 + random.Intn(3) {
					id := fmt.Sprintf("t%d_%d", turn, k)
					ids = append(ids, id)
					args := map[string]any{"q": "x"}
					switch random.Intn(8) {
					case 0:
						args = map[string]any{}
					case 1:
						args = map[string]any{"q": 5}
					}
					content = append(content, map[string]any{"type": "toolCall", "id": id, "name": pick("a", "a", "b", "c", "missing"), "arguments": args})
					switch random.Intn(6) {
					case 0:
						p.Before[id] = map[string]any{"block": true, "reason": pick("no", "")}
						if random.Intn(2) == 0 {
							p.Before[id]["terminate"] = true
						}
					case 1:
						p.Before[id] = map[string]any{}
					}
					if random.Intn(4) == 0 {
						after := map[string]any{}
						if random.Intn(2) == 0 {
							after["content"] = pick("none", "some") // replaced below
							if after["content"] == "none" {
								after["content"] = []any{}
							} else {
								after["content"] = []any{map[string]any{"type": "text", "text": "over"}}
							}
						}
						if random.Intn(2) == 0 {
							after["isError"] = random.Intn(2) == 0
						}
						if random.Intn(2) == 0 {
							after["terminate"] = random.Intn(2) == 0
						}
						if random.Intn(3) == 0 {
							after["details"] = map[string]any{"d": 1}
						}
						p.After[id] = after
					}
				}
			}
			if len(content) == 0 {
				content = append(content, map[string]any{"type": "text", "text": text()})
			}
			p.Turns = append(p.Turns, struct {
				Content []map[string]any `json:"content"`
				Stop    string           `json:"stop"`
				Stream  string           `json:"stream"`
			}{content, stop, pick("plain", "plain", "deltas", "nostart", "noend")})
		}
		if p.Turns == nil {
			p.Turns = make([]struct {
				Content []map[string]any `json:"content"`
				Stop    string           `json:"stop"`
				Stream  string           `json:"stream"`
			}, 0)
		}
		if len(ids) > 0 && random.Intn(6) == 0 {
			p.AbortAt = ids[random.Intn(len(ids))]
		}
		queue := func() [][]map[string]any {
			lists := [][]map[string]any{}
			for range random.Intn(5) {
				list := []map[string]any{}
				for range random.Intn(3) {
					list = append(list, user("q"+text()))
				}
				lists = append(lists, list)
			}
			return lists
		}
		p.Steering, p.FollowUps = queue(), queue()
		p.Finish = []*string{}
		for range random.Intn(6) {
			switch random.Intn(5) {
			case 0:
				p.Finish = append(p.Finish, new("end"))
			case 1:
				p.Finish = append(p.Finish, new("continue"))
			default:
				p.Finish = append(p.Finish, nil)
			}
		}
		for range 1 + random.Intn(2) {
			p.Prompts = append(p.Prompts, user(text()))
		}
		p.Context = []map[string]any{}
		if random.Intn(3) == 0 {
			p.Context = append(p.Context, user("earlier"))
		}
		// Poisoned history, as Pi hands it to the stream function: errored and aborted turns stay, orphaned calls get no synthetic result, orphaned and repeated results stay.
		assistant := func(stop string, ids ...string) map[string]any {
			content := []map[string]any{{"type": "text", "text": "old"}}
			for _, id := range ids {
				content = append(content, map[string]any{"type": "toolCall", "id": id, "name": "a", "arguments": map[string]any{"q": "x"}})
			}
			return map[string]any{"role": "assistant", "content": content, "stopReason": stop, "api": "faux", "provider": "faux", "model": "faux-1", "usage": map[string]any{}, "timestamp": 1}
		}
		result := func(id string) map[string]any {
			return map[string]any{"role": "toolResult", "toolCallId": id, "toolName": "a", "content": []map[string]any{{"type": "text", "text": "r"}}, "isError": false, "timestamp": 1}
		}
		if random.Intn(2) == 0 {
			poison := [][]map[string]any{
				{assistant("error", "e1"), result("e1")},
				{assistant("aborted", "g1", "g2"), result("g1")},
				{assistant("toolUse", "o1", "o2"), result("o1"), result("o1"), user("next")},
				{assistant("toolUse", "l1"), user("interrupt"), result("l1")},
				{result("stray"), assistant("stop")},
			}
			for range 1 + random.Intn(3) {
				p.Context = append(p.Context, poison[random.Intn(len(poison))]...)
			}
		}
		// System messages in the context, the prompts and the queues reach declareToolChanges' pending branch (agent-loop.ts:353-357): a pending system message's tool fields are intent, replaced by the delta from the committed transcript. A separate source keeps the programs above unchanged.
		if declared.Intn(4) == 0 {
			p.Context = append(p.Context, system())
		}
		// Up to two in one list: the last pending system message carries the intent (agent-loop.ts:334-339).
		insert := func(list []map[string]any) []map[string]any {
			for range 1 + declared.Intn(2) {
				list = slices.Insert(list, declared.Intn(len(list)+1), system())
			}
			return list
		}
		if declared.Intn(3) == 0 {
			p.Prompts = insert(p.Prompts)
		}
		for _, lists := range [][][]map[string]any{p.Steering, p.FollowUps} {
			for i := range lists {
				if declared.Intn(4) == 0 {
					lists[i] = insert(lists[i])
				}
			}
		}
		programs = append(programs, p)
	}
	return programs
}

var loopKeep = []string{"role", "content", "stopReason", "toolCallId", "toolName", "isError", "toolsAdded", "toolsRemoved", "thinkingLevel"}

// projectLoopMessage keeps the members the Pi driver keeps; an empty tool list is not mirrored by ai.SystemMessage.
func projectLoopMessage(t *testing.T, message any) map[string]any {
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var full map[string]any
	if err := json.Unmarshal(encoded, &full); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, key := range loopKeep {
		value, ok := full[key]
		if !ok || value == nil {
			continue
		}
		if list, isList := value.([]any); isList && len(list) == 0 && (key == "toolsAdded" || key == "toolsRemoved") {
			continue
		}
		out[key] = value
	}
	return out
}

func TestAgentLoopMatchesPi(t *testing.T) {
	programs := loopPrograms()
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/agent_loop_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%.2000s", err, &stderr)
	}
	var expected []map[string]any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	canonical := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		_ = json.Unmarshal(encoded, &out)
		return out
	}
	result := func(r AgentToolResult) map[string]any {
		content := []any{}
		for _, block := range r.Content {
			content = append(content, canonical(block))
		}
		out := map[string]any{"content": content, "isError": r.IsError, "terminate": r.Terminate}
		if r.Details != nil {
			out["details"] = canonical(r.Details)
		}
		return out
	}
	// normalize orders the events of a parallel batch that follow its last tool_execution_start by tool call: their order is completion order, which goroutines do not fix.
	normalize := func(events []any) []any {
		isTool := func(event any) (string, bool) {
			kind, _ := event.(map[string]any)["type"].(string)
			return kind, kind == "tool_execution_start" || kind == "tool_execution_update" || kind == "tool_execution_end"
		}
		for i := 0; i < len(events); {
			if _, ok := isTool(events[i]); !ok {
				i++
				continue
			}
			j, lastStart := i, i
			for j < len(events) {
				kind, ok := isTool(events[j])
				if !ok {
					break
				}
				if kind == "tool_execution_start" {
					lastStart = j
				}
				j++
			}
			tail := events[lastStart+1 : j]
			sort.SliceStable(tail, func(a, b int) bool {
				return tail[a].(map[string]any)["id"].(string) < tail[b].(map[string]any)["id"].(string)
			})
			i = j
		}
		return events
	}
	differing, eventCount, toolStarts, truncated, terminated, blocked := 0, 0, 0, 0, 0, 0
	ctx := context.Background()
	for i, p := range programs {
		var events []any
		var requests []any
		var tools []AgentTool
		var cancelRun context.CancelFunc
		for _, spec := range p.Tools {
			tools = append(tools, &loopTool{name: spec.Name, mode: spec.Mode, kind: spec.Behavior.Kind, text: spec.Behavior.Text, updates: spec.Behavior.Updates, terminate: spec.Behavior.Terminate, abort: abortFn(spec.Behavior.Abort, func() { cancelRun() })})
		}
		call := 0
		streamFn := func(streamCtx context.Context, _ *ai.Model, transcript ai.TranscriptContext, streamOptions ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			// pig divergence (D48): the request option that makes a real provider repair the tool results; the messages above stay Pi's.
			if !streamOptions.RepairToolFlow {
				t.Error("the loop did not ask the provider to repair the tool flow")
			}
			messages := []any{}
			for _, message := range transcript.Messages() {
				projected := projectLoopMessage(t, message)
				// Pig runs transformMessages for the target model when it builds the request; Pi runs it inside each provider, so Pi's stream function sees an orphaned tool call without the synthetic result (transform.go).
				if blocks, ok := projected["content"].([]any); ok && projected["role"] == "toolResult" && len(blocks) == 1 && blocks[0].(map[string]any)["text"] == "No result provided" {
					continue
				}
				messages = append(messages, projected)
			}
			requests = append(requests, messages)
			index := call
			call++
			turn := struct {
				Content []map[string]any
				Stop    string
				Stream  string
			}{[]map[string]any{{"type": "text", "text": "done"}}, "stop", "plain"}
			if index < len(p.Turns) {
				turn.Content, turn.Stop, turn.Stream = p.Turns[index].Content, p.Turns[index].Stop, p.Turns[index].Stream
			}
			// A provider honours an aborted signal with an aborted error message.
			aborted := streamCtx.Err() != nil
			if aborted {
				turn.Content, turn.Stop, turn.Stream = []map[string]any{}, "aborted", "plain"
			}
			assistant := func(content []map[string]any) *ai.AssistantMessage {
				wire := map[string]any{"role": "assistant", "content": content, "api": "faux", "provider": "faux", "model": "faux-1", "stopReason": turn.Stop, "timestamp": 1000 + index,
					"usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
				if turn.Stop == "error" {
					wire["errorMessage"] = "boom"
				}
				encoded, _ := json.Marshal(wire)
				var message ai.AssistantMessage
				if err := json.Unmarshal(encoded, &message); err != nil {
					t.Fatal(err)
				}
				return &message
			}
			stream := ai.NewAssistantMessageEventStream()
			final := assistant(turn.Content)
			push := func(event ai.AssistantMessageEvent) {
				if err := stream.Push(event); err != nil {
					t.Fatal(err)
				}
			}
			if turn.Stream != "nostart" && !aborted {
				push(ai.StartEvent{Partial: assistant([]map[string]any{})})
			}
			if turn.Stream == "deltas" {
				partial := []map[string]any{}
				for ci, block := range turn.Content {
					if block["type"] == "text" {
						partial = append(partial, map[string]any{"type": "text", "text": ""})
						push(ai.TextStartEvent{ContentIndex: ci, Partial: assistant(append([]map[string]any{}, partial...))})
						partial[ci] = block
						push(ai.TextDeltaEvent{ContentIndex: ci, Delta: block["text"].(string), Partial: assistant(append([]map[string]any{}, partial...))})
						push(ai.TextEndEvent{ContentIndex: ci, Content: block["text"].(string), Partial: assistant(append([]map[string]any{}, partial...))})
						continue
					}
					open := map[string]any{"type": "toolCall", "id": block["id"], "name": block["name"], "arguments": map[string]any{}}
					partial = append(partial, open)
					push(ai.ToolCallStartEvent{ContentIndex: ci, Partial: assistant(append([]map[string]any{}, partial...))})
					partial[ci] = block
					done := assistant(append([]map[string]any{}, partial...))
					push(ai.ToolCallEndEvent{ContentIndex: ci, ToolCall: done.Content[ci].(ai.ToolCall), Partial: done})
				}
			}
			switch {
			case turn.Stop == "error" || turn.Stop == "aborted":
				push(ai.ErrorEvent{Reason: ai.StopReason(turn.Stop), Error: final})
			case turn.Stream == "noend":
				stream.End(final)
			default:
				push(ai.DoneEvent{Reason: ai.StopReason(turn.Stop), Message: final})
			}
			return stream, nil
		}
		next := func(lists [][]map[string]any) func() []AgentMessage {
			n := 0
			return func() []AgentMessage {
				if n >= len(lists) {
					n++
					return nil
				}
				var out []AgentMessage
				for _, raw := range lists[n] {
					encoded, _ := json.Marshal(raw)
					var message AgentMessage
					if err := json.Unmarshal(encoded, &message); err != nil {
						t.Fatal(err)
					}
					out = append(out, message)
				}
				n++
				return out
			}
		}
		decode := func(raws []map[string]any) []AgentMessage {
			var out []AgentMessage
			for _, raw := range raws {
				encoded, _ := json.Marshal(raw)
				var message AgentMessage
				if err := json.Unmarshal(encoded, &message); err != nil {
					t.Fatal(err)
				}
				out = append(out, message)
			}
			return out
		}
		finishCall := 0
		config := AgentLoopConfig{
			Model:               &ai.Model{ID: "faux-1"},
			GetSteeringMessages: next(p.Steering),
			GetFollowUpMessages: next(p.FollowUps),
			FinishTurn: func(context.Context, AgentTurnContext) (*AgentTurnDecision, error) {
				decision := (*string)(nil)
				if finishCall < len(p.Finish) {
					decision = p.Finish[finishCall]
				}
				finishCall++
				if decision == nil {
					return nil, nil
				}
				return &AgentTurnDecision{Action: AgentTurnAction(*decision)}, nil
			},
			ToolExecution: ToolExecutionMode(p.Execution),
			BeforeToolCall: func(_ context.Context, call BeforeToolCallContext) *BeforeToolCallResult {
				if call.ToolCall.ID == p.AbortAt {
					cancelRun()
				}
				h, ok := p.Before[call.ToolCall.ID]
				if !ok {
					return nil
				}
				result := &BeforeToolCallResult{}
				result.Block, _ = h["block"].(bool)
				result.Reason, _ = h["reason"].(string)
				result.Terminate, _ = h["terminate"].(bool)
				return result
			},
			AfterToolCall: func(_ context.Context, call AfterToolCallContext) *AfterToolCallResult {
				h, ok := p.After[call.ToolCall.ID]
				if !ok {
					return nil
				}
				result := &AfterToolCallResult{}
				if content, ok := h["content"].([]any); ok {
					result.Content = []ai.ToolResultMessageContent{}
					for _, block := range content {
						result.Content = append(result.Content, ai.TextContent{Text: block.(map[string]any)["text"].(string)})
					}
				}
				if v, ok := h["isError"].(bool); ok {
					result.IsError = &v
				}
				if v, ok := h["terminate"].(bool); ok {
					result.Terminate = &v
				}
				if v, ok := h["details"]; ok {
					result.Details = v
				}
				return result
			},
		}
		sink := func(event AgentEvent) error {
			out := map[string]any{}
			switch e := event.(type) {
			case AgentStartEvent:
				out["type"] = "agent_start"
			case TurnStartEvent:
				out["type"] = "turn_start"
			case MessageStartEvent:
				out["type"], out["message"] = "message_start", projectLoopMessage(t, e.Message)
			case MessageEndEvent:
				out["type"], out["message"] = "message_end", projectLoopMessage(t, e.Message)
			case MessageUpdateEvent:
				out["type"], out["message"], out["sub"] = "message_update", projectLoopMessage(t, e.Message), string(e.AssistantMessageEvent.EventType())
			case ToolExecutionStartEvent:
				out["type"], out["id"], out["name"], out["args"] = "tool_execution_start", e.ToolCallID, e.ToolName, canonical(json.RawMessage(e.Args))
			case ToolExecutionUpdateEvent:
				out["type"], out["id"], out["name"], out["args"], out["partial"] = "tool_execution_update", e.ToolCallID, e.ToolName, canonical(json.RawMessage(e.Args)), result(e.PartialResult)
			case ToolExecutionEndEvent:
				out["type"], out["id"], out["name"], out["result"], out["isError"] = "tool_execution_end", e.ToolCallID, e.ToolName, result(e.Result), e.IsError
			case TurnEndEvent:
				results := []any{}
				for _, r := range e.ToolResults {
					results = append(results, projectLoopMessage(t, r))
				}
				out["type"], out["message"], out["toolResults"] = "turn_end", projectLoopMessage(t, e.Message), results
			case AgentEndEvent:
				messages := []any{}
				for _, m := range e.Messages {
					messages = append(messages, projectLoopMessage(t, m))
				}
				out["type"], out["messages"] = "agent_end", messages
			default:
				return nil
			}
			events = append(events, out)
			return nil
		}
		runCtx, cancel := context.WithCancel(ctx)
		cancelRun = cancel
		returned, runErr := RunAgentLoop(runCtx, decode(p.Prompts), AgentContext{Messages: decode(p.Context), Tools: tools}, config, sink, streamFn)
		messages := []any{}
		for _, m := range returned {
			messages = append(messages, projectLoopMessage(t, m))
		}
		want := expected[i]
		wantEvents, _ := want["events"].([]any)
		gotEvents := normalize(canonical(events).([]any))
		wantEvents = normalize(wantEvents)
		eventCount += len(gotEvents)
		for _, event := range gotEvents {
			if event.(map[string]any)["type"] == "tool_execution_start" {
				toolStarts++
			}
		}
		for _, turn := range p.Turns {
			if turn.Stop == "length" {
				truncated++
			}
		}
		terminated += len(p.Before)
		blocked += len(p.After)
		wantMessages, _ := want["messages"].([]any)
		wantRequests, _ := want["requests"].([]any)
		wantErr, wantHasErr := want["error"].(string)
		same := reflect.DeepEqual(gotEvents, wantEvents) && reflect.DeepEqual(canonical(messages), canonical(wantMessages)) && reflect.DeepEqual(canonical(requests), canonical(wantRequests)) && (runErr != nil) == wantHasErr
		if same && runErr != nil && runErr.Error() != wantErr {
			same = false
		}
		if !same {
			differing++
			if differing <= 3 {
				first := 0
				for first < len(gotEvents) && first < len(wantEvents) && reflect.DeepEqual(gotEvents[first], wantEvents[first]) {
					first++
				}
				at := func(events []any) string {
					if first >= len(events) {
						return "<end>"
					}
					encoded, _ := json.Marshal(events[first])
					return string(encoded)
				}
				var wantReq, gotReq []byte
				if first >= len(gotEvents) {
					wantReq, _ = json.Marshal(wantRequests)
					gotReq, _ = json.Marshal(requests)
					summary := func(requests []any) string {
						out := ""
						for _, request := range requests {
							out += "["
							for _, message := range request.([]any) {
								m := message.(map[string]any)
								out += fmt.Sprintf("%v/%v/%v/%v ", m["role"], m["stopReason"], m["toolCallId"], m["isError"])
							}
							out += "] "
						}
						return out
					}
					wr, gr := canonical(wantRequests).([]any), canonical(requests).([]any)
					k := 0
					for k < len(wr) && k < len(gr) && reflect.DeepEqual(wr[k], gr[k]) {
						k++
					}
					if k < len(wr) && k < len(gr) {
						t.Logf("%d vs %d requests; request %d differs\n  Pi  %s\n  Pig %s", len(wr), len(gr), k, summary(wr[k:k+1]), summary(gr[k:k+1]))
					} else {
						t.Logf("%d vs %d requests", len(wr), len(gr))
					}
					_, _ = wantReq, gotReq
				}
				t.Errorf("program %d abortAt %q: err %v / %q; %d vs %d events, first difference at %d\n  Pi  %.700s\n  Pig %.700s", i, p.AbortAt, runErr, wantErr, len(wantEvents), len(gotEvents), first, at(wantEvents), at(gotEvents))
			}
		}
	}
	t.Logf("%d programs, %d events, %d tool starts, %d truncated turns, %d before hooks, %d after hooks, %d differing", len(programs), eventCount, toolStarts, truncated, terminated, blocked, differing)
	if differing > 0 {
		t.Errorf("%d programs differ from Pi", differing)
	}
}
