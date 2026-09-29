package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func report(name string, values ...any) {
	data, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}
	fmt.Printf("AGENT_LIFECYCLE %s %s\n", name, data)
}

type probeTool struct{ name string }

func (t probeTool) Name() string  { return t.name }
func (t probeTool) Label() string { return t.name }
func (t probeTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: t.name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (probeTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (probeTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{}, nil
}

func main() {
	started, terminal, ended, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var signal context.Context
	var transcriptStates []string
	var subscriberFinished, promptFinished atomic.Bool
	a := agent.NewAgent(agent.AgentOptions{StreamFn: func(ctx context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		stream := ai.NewAssistantMessageEventStream()
		if err := stream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}}, StopReason: ai.StopReasonPending}}); err != nil {
			return nil, err
		}
		go func() {
			<-ctx.Done()
			close(terminal)
			if err := stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}}, StopReason: ai.StopReasonAborted, ErrorMessage: "Request was aborted"}}); err != nil {
				panic(err)
			}
		}()
		close(started)
		return stream, nil
	}})
	a.Subscribe(func(ctx context.Context, event agent.AgentEvent) error {
		var messageRole string
		var kind string
		switch e := event.(type) {
		case agent.MessageStartEvent:
			kind, messageRole = "message_start", e.Message.Role()
		case agent.MessageEndEvent:
			kind, messageRole = "message_end", e.Message.Role()
		}
		if kind != "" {
			var roles []string
			for _, message := range a.MessagesSnapshot() {
				roles = append(roles, message.Role())
			}
			transcriptStates = append(transcriptStates, kind+":"+messageRole+":"+strings.Join(roles, ","))
		}
		switch event.(type) {
		case agent.AgentStartEvent:
			signal = ctx
		case agent.AgentEndEvent:
			close(ended)
			<-release
			subscriberFinished.Store(true)
		}
		return nil
	})
	prompt := make(chan struct{})
	go func() {
		defer close(prompt)
		if _, err := a.Send(context.Background(), "abort me"); err != nil {
			panic(err)
		}
		promptFinished.Store(true)
	}()
	<-started
	if signal.Err() != nil {
		panic("signal was canceled before Abort")
	}
	a.Abort()
	<-terminal
	<-ended
	if signal.Err() == nil || promptFinished.Load() || subscriberFinished.Load() || !a.IsStreaming() {
		panic("run settled before agent_end callback")
	}
	_, promptError := a.Send(context.Background(), "busy")
	_, continueError := a.Continue(context.Background())
	resetError := a.Reset()
	if promptError == nil || continueError == nil || resetError == nil {
		panic("busy operation succeeded")
	}
	runErrors := []string{promptError.Error(), continueError.Error(), resetError.Error()}
	messages := a.MessagesSnapshot()
	last := messages[len(messages)-1].Assistant
	report("held", string(last.StopReason), last.ErrorMessage, a.ErrorMessage(), signal.Err() != nil, promptFinished.Load())
	idle := make(chan struct{})
	go func() { a.WaitForIdle(); close(idle) }()
	close(release)
	<-prompt
	<-idle
	pending := append([]string{}, a.PendingToolCalls()...)
	report("settled", subscriberFinished.Load(), promptFinished.Load(), a.IsStreaming(), pending)
	empty := agent.NewAgent(agent.AgentOptions{})
	_, emptyError := empty.Continue(context.Background())
	_, assistantError := a.Continue(context.Background())
	if emptyError == nil || assistantError == nil {
		panic("invalid continuation succeeded")
	}
	runErrors = append(runErrors, emptyError.Error(), assistantError.Error())
	data, err := json.Marshal(runErrors)
	if err != nil {
		panic(err)
	}
	fmt.Printf("AGENT_LIFECYCLE errors %s\n", data)
	data, err = json.Marshal(transcriptStates)
	if err != nil {
		panic(err)
	}
	fmt.Printf("AGENT_LIFECYCLE transcript %s\n", data)
	data, err = json.Marshal(a.ErrorMessage())
	if err != nil {
		panic(err)
	}
	fmt.Printf("AGENT_LIFECYCLE retained-error %s\n", data)
	initialTools := []agent.AgentTool{probeTool{name: "first"}}
	copied := agent.NewAgent(agent.AgentOptions{Tools: initialTools})
	initialTools[0] = probeTool{name: "second"}
	report("initial-tools", copied.Tools()[0].Name(), copied.Messages()[0].System.ToolsAdded[0].Name)
}
