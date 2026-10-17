package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// pi: packages/agent/src/agent-loop.ts agentLoop

// The standalone loop against pinned Pi over random scripts: assistant turns of text and tool calls (known, unknown and badly typed calls, several per
// message), stop reasons, tools that return, fail, report an error, terminate or take time, parallel and sequential batches with per-tool modes, steering
// and follow-up queues, beforeToolCall blocks and afterToolCall overrides, and prior history. The events and the returned messages must be equal after
// the clock, usage and provider fields are removed.
func TestAgentLoopMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(193))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	toolNames := []string{"echo", "slow", "fail", "iserr", "term", "strict", "upd"}
	type call struct {
		ID   string         `json:"id"`
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	type turn struct {
		Text  string `json:"text"`
		Calls []call `json:"calls"`
		Stop  string `json:"stop"`
	}
	type afterOverride struct {
		Text      string `json:"text"`
		IsError   *bool  `json:"isError"`
		Terminate *bool  `json:"terminate"`
	}
	type probe struct {
		Mode      string                   `json:"mode"`
		ToolModes map[string]string        `json:"toolModes,omitempty"`
		Tools     []string                 `json:"tools"`
		Turns     []turn                   `json:"turns"`
		Steering  [][]string               `json:"steering"`
		FollowUp  [][]string               `json:"followUp"`
		Block     map[string]string        `json:"block,omitempty"`
		After     map[string]afterOverride `json:"after,omitempty"`
		Stream    bool                     `json:"stream"`
		History   []string                 `json:"history"`
		Prompt    string                   `json:"prompt"`
	}
	var probes []probe
	ids := 0
	for range 400 {
		p := probe{Mode: pick("", "parallel", "sequential"), Prompt: pick("go", "hello", "again"), Steering: [][]string{}, FollowUp: [][]string{}, History: []string{}}
		for _, name := range toolNames {
			if rng.Intn(5) > 0 {
				p.Tools = append(p.Tools, name)
			}
		}
		if len(p.Tools) == 0 {
			p.Tools = []string{"echo"}
		}
		if rng.Intn(4) == 0 {
			p.ToolModes = map[string]string{p.Tools[rng.Intn(len(p.Tools))]: pick("sequential", "parallel")}
		}
		for range rng.Intn(5) {
			t := turn{Stop: pick("toolUse", "toolUse", "toolUse", "stop", "stop", "error", "aborted", "length")}
			if rng.Intn(2) == 0 {
				t.Text = pick("thinking out loud", "ok", "x")
			}
			if t.Stop != "stop" || rng.Intn(4) == 0 {
				for range 1 + rng.Intn(3) {
					ids++
					c := call{ID: fmt.Sprintf("call_%d", ids), Name: pick(append([]string{"nosuch"}, toolNames...)...), Args: map[string]any{}}
					switch c.Name {
					case "echo", "slow":
						if rng.Intn(3) > 0 {
							c.Args["text"] = pick("a", "b c", "")
						}
						if rng.Intn(10) == 0 {
							c.Args["text"] = 5
						}
					case "strict":
						switch rng.Intn(4) {
						case 0:
							c.Args["n"] = "not a number"
						case 1:
						default:
							c.Args["n"] = rng.Intn(10)
						}
					}
					t.Calls = append(t.Calls, c)
				}
			}
			p.Turns = append(p.Turns, t)
		}
		for range rng.Intn(3) {
			p.Steering = append(p.Steering, []string{})
			for range rng.Intn(3) {
				p.Steering[len(p.Steering)-1] = append(p.Steering[len(p.Steering)-1], pick("steer a", "steer b"))
			}
		}
		for range rng.Intn(3) {
			p.FollowUp = append(p.FollowUp, []string{})
			for range rng.Intn(3) {
				p.FollowUp[len(p.FollowUp)-1] = append(p.FollowUp[len(p.FollowUp)-1], pick("follow a", "follow b"))
			}
		}
		if rng.Intn(5) == 0 {
			p.Block = map[string]string{pick(toolNames...): pick("not allowed", "")}
		}
		if rng.Intn(5) == 0 {
			override := afterOverride{Text: pick("overridden", "")}
			if rng.Intn(2) == 0 {
				override.IsError = new(rng.Intn(2) == 0)
			}
			if rng.Intn(2) == 0 {
				override.Terminate = new(rng.Intn(2) == 0)
			}
			p.After = map[string]afterOverride{pick(toolNames...): override}
		}
		p.Stream = rng.Intn(2) == 0
		for range rng.Intn(3) {
			p.History = append(p.History, pick("earlier", "reply"))
		}
		probes = append(probes, p)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/agent_loop.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Events   []any `json:"events"`
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, p := range probes {
		var events []any
		var returned []agent.AgentMessage
		var tools []agent.AgentTool
		for _, name := range p.Tools {
			tools = append(tools, loopOracleTool{name: name, mode: agent.ToolExecutionMode(p.ToolModes[name])})
		}
		turnIndex := 0
		streamFn := func(_ context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			script := turn{Text: "done", Stop: "stop"}
			if turnIndex < len(p.Turns) {
				script = p.Turns[turnIndex]
			}
			turnIndex++
			var content []ai.AssistantContentBlock
			if script.Text != "" {
				content = append(content, ai.TextContent{Text: script.Text})
			}
			for _, c := range script.Calls {
				content = append(content, ai.ToolCall{ID: c.ID, Name: c.Name, Arguments: ai.JsonObject(c.Args)})
			}
			message := &ai.AssistantMessage{Content: content, API: "test", Provider: "test", Model: "mock", StopReason: ai.StopReason(script.Stop)}
			stream := ai.NewAssistantMessageEventStream()
			_ = stream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{API: "test", Provider: "test", Model: "mock", StopReason: ai.StopReasonPending}})
			if p.Stream {
				var done []ai.AssistantContentBlock
				snapshot := func(current ai.AssistantContentBlock) *ai.AssistantMessage {
					content := append([]ai.AssistantContentBlock(nil), done...)
					if current != nil {
						content = append(content, current)
					}
					return &ai.AssistantMessage{Content: content, API: "test", Provider: "test", Model: "mock", StopReason: ai.StopReasonPending}
				}
				for _, block := range content {
					index := len(done)
					switch b := block.(type) {
					case ai.TextContent:
						_ = stream.Push(ai.TextStartEvent{ContentIndex: index, Partial: snapshot(ai.TextContent{})})
						_ = stream.Push(ai.TextDeltaEvent{ContentIndex: index, Delta: b.Text, Partial: snapshot(b)})
						_ = stream.Push(ai.TextEndEvent{ContentIndex: index, Content: b.Text, Partial: snapshot(b)})
					case ai.ToolCall:
						_ = stream.Push(ai.ToolCallStartEvent{ContentIndex: index, Partial: snapshot(ai.ToolCall{ID: b.ID, Name: b.Name, Arguments: ai.JsonObject{}})})
						arguments, _ := json.Marshal(b.Arguments)
						_ = stream.Push(ai.ToolCallDeltaEvent{ContentIndex: index, Delta: string(arguments), Partial: snapshot(b)})
						_ = stream.Push(ai.ToolCallEndEvent{ContentIndex: index, ToolCall: b, Partial: snapshot(b)})
					}
					done = append(done, block)
				}
			}
			if script.Stop == "error" || script.Stop == "aborted" {
				message.ErrorMessage = script.Stop
				_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReason(script.Stop), Error: message})
			} else {
				_ = stream.Push(ai.DoneEvent{Reason: ai.StopReason(script.Stop), Message: message})
			}
			return stream, nil
		}
		steering, followUp := p.Steering, p.FollowUp
		queue := func(batches *[][]string) []agent.AgentMessage {
			if len(*batches) == 0 {
				return nil
			}
			batch := (*batches)[0]
			*batches = (*batches)[1:]
			var out []agent.AgentMessage
			for _, text := range batch {
				out = append(out, loopUserMessage(text))
			}
			return out
		}
		config := agent.AgentLoopConfig{
			Model:               &ai.Model{ID: "mock", DisplayName: "mock", Capabilities: ai.ModelCapabilities{ContextWindow: 8192}},
			ToolExecution:       agent.ToolExecutionMode(p.Mode),
			GetSteeringMessages: func() []agent.AgentMessage { return queue(&steering) },
			GetFollowUpMessages: func() []agent.AgentMessage { return queue(&followUp) },
			BeforeToolCall: func(_ context.Context, c agent.BeforeToolCallContext) *agent.BeforeToolCallResult {
				if reason := p.Block[c.ToolCall.Name]; reason != "" {
					return &agent.BeforeToolCallResult{Block: true, Reason: reason}
				}
				return nil
			},
			AfterToolCall: func(_ context.Context, c agent.AfterToolCallContext) *agent.AfterToolCallResult {
				override, ok := p.After[c.ToolCall.Name]
				if !ok {
					return nil
				}
				result := &agent.AfterToolCallResult{IsError: override.IsError, Terminate: override.Terminate}
				if override.Text != "" {
					result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: override.Text}}
				}
				return result
			},
		}
		var history []agent.AgentMessage
		for index, text := range p.History {
			if index%2 == 0 {
				history = append(history, loopUserMessage(text))
			} else {
				history = append(history, loopAssistantText(text))
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
		var err error
		returned, err = agent.RunAgentLoop(ctx, []agent.AgentMessage{loopUserMessage(p.Prompt)}, agent.AgentContext{Messages: history, Tools: tools}, config, func(event agent.AgentEvent) error {
			frames, err := rpcAgentEvent(event)
			if err != nil {
				return err
			}
			for _, frame := range frames {
				raw, _ := json.Marshal(frame)
				var decoded any
				_ = json.Unmarshal(raw, &decoded)
				events = append(events, decoded)
			}
			return nil
		}, streamFn)
		cancel()
		if err != nil {
			events = append(events, map[string]any{"type": "thrown", "message": err.Error()})
		}
		var messages []any
		for _, m := range returned {
			wire, err := rpcAgentMessage(m)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(wire)
			var decoded any
			_ = json.Unmarshal(raw, &decoded)
			messages = append(messages, decoded)
		}
		normalize := func(events any) any { return events }
		if p.Mode != "sequential" {
			normalize = loopGroupToolEvents
		}
		got, _ := json.Marshal(map[string]any{"events": normalize(loopStrip(events)), "messages": loopStrip(messages)})
		expected, _ := json.Marshal(map[string]any{"events": normalize(loopStrip(want[i].Events)), "messages": loopStrip(want[i].Messages)})
		if !bytes.Equal(got, expected) {
			if failures++; failures <= 4 {
				first := 0
				for first < len(got) && first < len(expected) && got[first] == expected[first] {
					first++
				}
				from := max(first-150, 0)
				t.Errorf("probe %d: %s\n PiG …%s\n Pi  …%s", i, loopOracleJSON(p), got[from:min(first+350, len(got))], expected[from:min(first+350, len(expected))])
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

func loopOracleJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// loopStrip removes what differs between runs (clock, usage, provider identity) and members that are null or absent in one encoding.
func loopStrip(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range v {
			switch key {
			case "timestamp", "usage", "api", "provider", "model", "durationMs", "thinkingLevel", "responseId", "partial", "willRetry":
				continue
			}
			// A hook that answers terminate: false makes Pi keep the member in the result; PiG's bool cannot tell false from unset.
			if child == nil || key == "terminate" && child == false {
				continue
			}
			out[key] = loopStrip(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = loopStrip(child)
		}
		return out
	default:
		return value
	}
}

type loopOracleTool struct {
	name string
	mode agent.ToolExecutionMode
}

func (t loopOracleTool) Name() string  { return t.name }
func (t loopOracleTool) Label() string { return t.name }
func (t loopOracleTool) Schema() ai.ToolSchema {
	properties := map[string]any{"text": map[string]any{"type": "string"}}
	required := []string{}
	switch t.name {
	case "fail", "iserr", "term", "upd":
		properties = map[string]any{}
	case "strict":
		properties, required = map[string]any{"n": map[string]any{"type": "number"}}, []string{"n"}
	}
	parameters := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		parameters["required"] = required
	}
	return ai.ToolSchema{Name: t.name, Description: t.name, Parameters: parameters}
}
func (t loopOracleTool) ExecutionMode() agent.ToolExecutionMode { return t.mode }
func (t loopOracleTool) Execute(ctx context.Context, _ string, params json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var args struct {
		Text *string `json:"text"`
		N    *float64
	}
	_ = json.Unmarshal(params, &args)
	text := func(value string) agent.AgentToolResult {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: value}}, Details: map[string]any{}}
	}
	value := ""
	if args.Text != nil {
		value = *args.Text
	}
	switch t.name {
	case "echo":
		return text("echo:" + value), nil
	case "slow":
		select {
		case <-time.After(15 * time.Millisecond):
		case <-ctx.Done():
		}
		return text("slow:" + value), nil
	case "upd":
		onUpdate(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "half"}}, Details: map[string]any{"step": 1}})
		onUpdate(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "most"}}, Details: map[string]any{"step": 2}})
		return text("upd done"), nil
	case "fail":
		return agent.AgentToolResult{}, errors.New("boom")
	case "iserr":
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "bad"}}, Details: map[string]any{"code": 1}, IsError: true}, nil
	case "term":
		result := text("stop now")
		result.Terminate = true
		return result, nil
	default:
		var n float64
		if args.N != nil {
			n = *args.N
		}
		return text(strings.TrimSpace(fmt.Sprintf("n=%v", n))), nil
	}
}

func loopUserMessage(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli()}}
}

func loopAssistantText(text string) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: ai.StopReasonStop}}
}

// loopGroupToolEvents keeps each run of tool_execution_* events' starts in their order and sorts the other events by tool call id, keeping each call's
// own events in order. A parallel batch emits every start in source order (agent-loop.ts executeToolCallsParallel) and then reports its calls as they
// progress: Pi's calls interleave on one thread (a microtask per await), PiG's on goroutines, so after the starts only the order within one call is
// comparable. The caller compares a sequential batch as it is.
func loopGroupToolEvents(value any) any {
	events, ok := value.([]any)
	if !ok {
		return value
	}
	inBatch := func(event any) bool {
		m, _ := event.(map[string]any)
		kind, _ := m["type"].(string)
		return strings.HasPrefix(kind, "tool_execution_")
	}
	out := append([]any(nil), events...)
	for start := 0; start < len(out); {
		if !inBatch(out[start]) {
			start++
			continue
		}
		end := start
		for end < len(out) && inBatch(out[end]) {
			end++
		}
		var starts, rest []any
		for _, event := range out[start:end] {
			if event.(map[string]any)["type"] == "tool_execution_start" {
				starts = append(starts, event)
			} else {
				rest = append(rest, event)
			}
		}
		slices.SortStableFunc(rest, func(a, b any) int {
			return strings.Compare(a.(map[string]any)["toolCallId"].(string), b.(map[string]any)["toolCallId"].(string))
		})
		copy(out[start:end], append(starts, rest...))
		start = end
	}
	return out
}
