package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type classAction struct {
	Kind  string   `json:"kind"`
	Text  string   `json:"text"`
	Texts []string `json:"texts"`
	Mode  string   `json:"mode"`
}

type classListener struct {
	Snapshot      bool `json:"snapshot"`
	UnsubscribeAt *int `json:"unsubscribeAt"`
	Action        *struct {
		At  int         `json:"at"`
		Act classAction `json:"act"`
	} `json:"action"`
}

type classProgram struct {
	SystemPrompt *string          `json:"systemPrompt"`
	Messages     []map[string]any `json:"messages"`
	Tools        []string         `json:"tools"`
	SteeringMode string           `json:"steeringMode"`
	FollowUpMode string           `json:"followUpMode"`
	Turns        []struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"turns"`
	Listeners []classListener `json:"listeners"`
	Ops       []classAction   `json:"ops"`
}

func classPrograms() []classProgram {
	random := rand.New(rand.NewSource(20261031))
	user := func(text string) map[string]any {
		return map[string]any{"role": "user", "content": text, "timestamp": 5}
	}
	assistant := func(stop string) map[string]any {
		out := map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "earlier"}}, "api": "faux", "provider": "faux", "model": "faux-1", "stopReason": stop, "timestamp": 1,
			"usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
		if stop == "error" {
			out["errorMessage"] = "earlier boom"
		}
		return out
	}
	word := func() string { return []string{"alpha", "beta", "gamma", "delta"}[random.Intn(4)] }
	action := func(inListener bool) classAction {
		var kind string
		if inListener {
			kind = []string{"prompt", "promptMessages", "continue", "steer", "followUp", "clearSteering", "clearFollowUp", "clearAll", "steeringMode", "followUpMode", "reset", "abort", "throw"}[random.Intn(13)]
		} else {
			kind = []string{"prompt", "prompt", "prompt", "promptMessages", "continue", "continue", "steer", "steer", "followUp", "followUp", "clearSteering", "clearFollowUp", "clearAll", "steeringMode", "followUpMode", "reset", "abort", "waitForIdle"}[random.Intn(18)]
		}
		a := classAction{Kind: kind, Text: word(), Texts: []string{}, Mode: []string{"all", "one-at-a-time"}[random.Intn(2)]}
		if kind == "promptMessages" {
			for range random.Intn(3) {
				a.Texts = append(a.Texts, word())
			}
		}
		return a
	}
	var programs []classProgram
	for range 3000 {
		var p classProgram
		switch random.Intn(5) {
		case 0:
			s := ""
			p.SystemPrompt = &s
		case 1, 2:
			s := "You are " + word() + "."
			p.SystemPrompt = &s
		}
		p.Tools, p.Listeners = []string{}, []classListener{}
		if random.Intn(3) == 0 {
			p.Tools = []string{"a"}
		}
		p.Messages = []map[string]any{}
		if random.Intn(8) == 0 {
			p.Messages = append(p.Messages, map[string]any{"role": "system", "content": "preset", "timestamp": 1})
		}
		switch random.Intn(6) {
		case 0:
			p.Messages = append(p.Messages, user("earlier"))
		case 1:
			p.Messages = append(p.Messages, user("earlier"), assistant("stop"))
		case 2:
			p.Messages = append(p.Messages, user("earlier"), assistant("error"))
		}
		if random.Intn(3) == 0 {
			p.SteeringMode = "all"
		}
		if random.Intn(3) == 0 {
			p.FollowUpMode = "all"
		}
		for range 3 + random.Intn(8) {
			kind := []string{"text", "text", "text", "text", "text", "text", "tool", "tool", "error", "error"}[random.Intn(10)]
			p.Turns = append(p.Turns, struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			}{kind, word()})
		}
		for range random.Intn(4) {
			l := classListener{Snapshot: random.Intn(2) == 0}
			if random.Intn(5) == 0 {
				n := random.Intn(10)
				l.UnsubscribeAt = &n
			}
			if random.Intn(3) == 0 {
				l.Action = &struct {
					At  int         `json:"at"`
					Act classAction `json:"act"`
				}{At: random.Intn(10), Act: action(true)}
			}
			p.Listeners = append(p.Listeners, l)
		}
		for range 2 + random.Intn(7) {
			p.Ops = append(p.Ops, action(false))
		}
		programs = append(programs, p)
	}
	return programs
}

var classKeep = []string{"role", "content", "stopReason", "errorMessage", "toolCallId", "toolName", "isError", "toolsAdded", "toolsRemoved", "api", "provider", "model", "usage"}

func projectClassMessage(t *testing.T, message any) any {
	if reflect.ValueOf(message).Kind() == reflect.Pointer && reflect.ValueOf(message).IsNil() {
		return nil
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var full map[string]any
	if err := json.Unmarshal(encoded, &full); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, key := range classKeep {
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

// classMessageText is Pi's text(message): a string content, or the joined text blocks.
func classMessageText(t *testing.T, message AgentMessage) string {
	encoded, _ := json.Marshal(message)
	var full struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(encoded, &full); err != nil {
		t.Fatal(err)
	}
	var s string
	if json.Unmarshal(full.Content, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(full.Content, &blocks)
	out := ""
	for _, block := range blocks {
		out += block.Text
	}
	return out
}

type classProvider struct{}

func (classProvider) ID() string { return "faux" }
func (classProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return nil, errors.New("unused")
}
func (classProvider) Close() error { return nil }

func classEventType(event AgentEvent) string {
	switch event.(type) {
	case AgentStartEvent:
		return "agent_start"
	case AgentEndEvent:
		return "agent_end"
	case TurnStartEvent:
		return "turn_start"
	case TurnEndEvent:
		return "turn_end"
	case MessageStartEvent:
		return "message_start"
	case MessageUpdateEvent:
		return "message_update"
	case MessageEndEvent:
		return "message_end"
	case ToolExecutionStartEvent:
		return "tool_execution_start"
	case ToolExecutionUpdateEvent:
		return "tool_execution_update"
	case ToolExecutionEndEvent:
		return "tool_execution_end"
	}
	return ""
}

// TestAgentClassMatchesPi runs Pi's Agent class (packages/agent/src/agent.ts) and Pig's Agent over seeded programs of operations, listeners that unsubscribe, act and fail, and scripted turns, and compares each operation's error and state and every listener event with its state.
// A stream function that returns an error is not generated: Pig answers it with an error assistant message for its retry loop where Pi's loop rejects (agent_loop.go requestStream).
func TestAgentClassMatchesPi(t *testing.T) {
	programs := classPrograms()
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/agent_class_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
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
	userMessage := func(text string) AgentMessage {
		return decode([]map[string]any{{"role": "user", "content": text, "timestamp": 5}})[0]
	}
	model := &ai.Model{ID: "faux-1", Provider: classProvider{}, ProviderMeta: ai.ProviderMetadata{ProviderID: "faux", API: "faux"}}
	differing, events, runs, failures, opErrors := 0, 0, 0, 0, 0
	for i, p := range programs {
		ctx := context.Background()
		call := 0
		var a *Agent
		streamFn := func(streamCtx context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			index := call
			call++
			turn := struct{ Kind, Text string }{"text", "done"}
			if index < len(p.Turns) {
				turn = struct{ Kind, Text string }{p.Turns[index].Kind, p.Turns[index].Text}
			}
			if turn.Kind == "throw" {
				return nil, errors.New(turn.Text)
			}
			aborted := streamCtx.Err() != nil
			stop, content, extra := "stop", []map[string]any{{"type": "text", "text": turn.Text}}, map[string]any{}
			switch {
			case aborted:
				stop, content = "aborted", []map[string]any{}
			case turn.Kind == "error":
				stop, extra["errorMessage"] = "error", turn.Text
			case turn.Kind == "tool":
				stop, content = "toolUse", []map[string]any{{"type": "toolCall", "id": fmt.Sprintf("t%d", index), "name": "a", "arguments": map[string]any{"q": "x"}}}
			}
			build := func(content []map[string]any, extra map[string]any) *ai.AssistantMessage {
				wire := map[string]any{"role": "assistant", "content": content, "api": "faux", "provider": "faux", "model": "faux-1", "stopReason": stop, "timestamp": 1000 + index,
					"usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
				maps.Copy(wire, extra)
				encoded, _ := json.Marshal(wire)
				var message ai.AssistantMessage
				if err := json.Unmarshal(encoded, &message); err != nil {
					t.Fatal(err)
				}
				return &message
			}
			stream := ai.NewAssistantMessageEventStream()
			final := build(content, extra)
			push := func(event ai.AssistantMessageEvent) {
				if err := stream.Push(event); err != nil {
					t.Fatal(err)
				}
			}
			if !aborted {
				push(ai.StartEvent{Partial: build([]map[string]any{}, nil)})
			}
			if stop == "error" || stop == "aborted" {
				push(ai.ErrorEvent{Reason: ai.StopReason(stop), Error: final})
			} else {
				push(ai.DoneEvent{Reason: ai.StopReason(stop), Message: final})
			}
			return stream, nil
		}
		var tools []AgentTool
		for _, name := range p.Tools {
			tools = append(tools, &loopTool{name: name, text: "ok"})
		}
		initial := &AgentInitialState{Model: model, Tools: tools, Messages: decode(p.Messages)}
		if p.SystemPrompt != nil {
			initial.SystemPrompt = *p.SystemPrompt
		}
		a, err = NewAgent(AgentOptions{InitialState: initial, StreamFn: streamFn, SteeringMode: QueueMode(p.SteeringMode), FollowUpMode: QueueMode(p.FollowUpMode), ToolExecution: ToolModeSequential})
		if err != nil {
			t.Fatal(err)
		}
		snapshot := func() map[string]any {
			messages := []any{}
			for _, message := range a.MessagesSnapshot() {
				messages = append(messages, projectClassMessage(t, message))
			}
			var streaming any
			if message := a.StreamingMessage(); message != nil {
				streaming = projectClassMessage(t, *message)
			}
			pending := a.PendingToolCalls()
			slices.Sort(pending)
			peek := []any{}
			for _, message := range a.PeekQueuedMessages() {
				peek = append(peek, classMessageText(t, message))
			}
			var errorMessage any
			if text := a.ErrorMessage(); text != "" {
				errorMessage = text
			}
			return map[string]any{"messages": messages, "systemPrompt": a.SystemPrompt(), "isStreaming": a.IsStreaming(), "streaming": streaming, "pending": canonical(append([]string{}, pending...)),
				"errorMessage": errorMessage, "hasSignal": a.Signal() != nil, "queued": a.HasPendingMessages(), "peek": peek, "modes": []any{string(a.SteeringMode()), string(a.FollowUpMode())}}
		}
		act := func(action classAction) error {
			switch action.Kind {
			case "prompt":
				return a.Prompt(ctx, action.Text)
			case "promptMessages":
				var messages []AgentMessage
				for _, text := range action.Texts {
					messages = append(messages, userMessage(text))
				}
				return a.PromptMessages(ctx, messages)
			case "continue":
				return a.Continue(ctx)
			case "steer":
				a.Steer(userMessage(action.Text))
			case "followUp":
				a.FollowUp(userMessage(action.Text))
			case "clearSteering":
				a.ClearSteeringQueue()
			case "clearFollowUp":
				a.ClearFollowUpQueue()
			case "clearAll":
				a.ClearAllQueues()
			case "steeringMode":
				a.SetSteeringMode(QueueMode(action.Mode))
			case "followUpMode":
				a.SetFollowUpMode(QueueMode(action.Mode))
			case "reset":
				return a.Reset()
			case "abort":
				a.Abort()
			case "waitForIdle":
				a.WaitForIdle()
			case "throw":
				return errors.New(action.Text)
			}
			return nil
		}
		var log []any
		counters := make([]int, len(p.Listeners))
		unsubscribers := make([]func(), len(p.Listeners))
		for li, spec := range p.Listeners {
			unsubscribers[li] = a.Subscribe(func(listenerCtx context.Context, event AgentEvent) error {
				kind := classEventType(event)
				if kind == "" {
					return nil
				}
				n := counters[li]
				counters[li]++
				entry := map[string]any{"listener": li, "n": n, "type": kind, "aborted": listenerCtx.Err() != nil}
				if spec.Snapshot {
					entry["snap"] = snapshot()
				}
				log = append(log, entry)
				if spec.UnsubscribeAt != nil && *spec.UnsubscribeAt == n {
					unsubscribers[li]()
				}
				if spec.Action != nil && spec.Action.At == n {
					return act(spec.Action.Act)
				}
				return nil
			})
		}
		var ops []any
		for _, op := range p.Ops {
			entry := map[string]any{}
			if err := act(op); err != nil {
				entry["error"] = err.Error()
				opErrors++
			}
			entry["state"] = snapshot()
			ops = append(ops, entry)
		}
		if log == nil {
			log = []any{}
		}
		got := canonical(map[string]any{"log": log, "ops": ops, "requests": call}).(map[string]any)
		want := canonical(expected[i])
		events += len(log)
		runs += call
		if !reflect.DeepEqual(got, want) {
			differing++
			if failures++; failures <= 2 {
				gj, _ := json.Marshal(got)
				wj, _ := json.Marshal(want)
				at := 0
				for at < len(gj) && at < len(wj) && gj[at] == wj[at] {
					at++
				}
				from := max(at-200, 0)
				names := func(v any) []string {
					var out []string
					for _, entry := range v.(map[string]any)["log"].([]any) {
						e := entry.(map[string]any)
						out = append(out, fmt.Sprintf("%v:%v", e["listener"], e["type"]))
					}
					return out
				}
				t.Errorf("program %d differs at byte %d\nprogram %+v\ngo : ...%.500s\npi : ...%.500s\ngo events %v\npi events %v", i, at, p, gj[from:], wj[from:], names(got), names(want))
			}
		}
	}
	t.Logf("%d programs, %d listener events, %d provider requests, %d op errors, %d differing", len(programs), events, runs, opErrors, differing)
	if differing > 0 {
		t.Fatalf("%d of %d programs differ from Pi", differing, len(programs))
	}
	if events < 5000 || opErrors < 500 {
		t.Fatalf("probe too narrow: %d events, %d op errors", events, opErrors)
	}
}
