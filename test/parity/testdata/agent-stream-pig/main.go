package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func reply(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	stream := ai.NewAssistantMessageEventStream()
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "fallback"}}, StopReason: ai.StopReasonStop}
	if err := stream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{StopReason: ai.StopReasonPending}}); err != nil {
		return nil, err
	}
	if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}); err != nil {
		return nil, err
	}
	return stream, nil
}
func main() {
	ctx := context.Background()
	calls := 0
	agent.SetDefaultStreamFn(func(ctx context.Context, m *ai.Model, c ai.TranscriptContext, o ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		calls++
		return reply(ctx, m, c, o)
	})
	a := newAgent(agent.AgentOptions{})
	if _, err := a.Send(ctx, "Hello"); err != nil {
		panic(err)
	}
	agent.SetDefaultStreamFn(nil)
	if calls != 1 {
		panic("default was not called exactly once")
	}
	fmt.Printf("AGENT_STREAM default %d\n", calls)
	for _, failed := range []bool{false, true} {
		stream := reply
		if failed {
			stream = func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return nil, errors.New("provider exploded")
			}
		}
		a := newAgent(agent.AgentOptions{Model: &ai.Model{ID: "mock", ProviderMeta: ai.ProviderMetadata{ProviderID: "openai", API: ai.APIOpenAIResponses}}, StreamFn: stream})
		messages, err := a.Send(ctx, "Hello")
		if err != nil {
			panic(err)
		}
		last := messages[len(messages)-1].Assistant
		text := last.ErrorMessage
		if !failed {
			text = last.Content[0].(ai.TextContent).Text
		}
		data, err := json.Marshal([]string{string(last.StopReason), text})
		if err != nil {
			panic(err)
		}
		fmt.Printf("AGENT_STREAM descriptor %s\n", data)
	}
	var convertedText string
	custom := newAgent(agent.AgentOptions{Model: &ai.Model{ID: "mock", ProviderMeta: ai.ProviderMetadata{ProviderID: "openai", API: ai.APIOpenAIResponses}},
		ConvertToLlm: func(messages []agent.AgentMessage) ([]ai.Message, error) {
			return []ai.Message{ai.UserMessage{Content: ai.UserText(messages[0].Custom["text"].(string)), Timestamp: 1}}, nil
		},
		StreamFn: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			convertedText = string(transcript.Messages()[0].(ai.UserMessage).Content.(ai.UserText))
			return reply(ctx, model, transcript, options)
		},
	})
	custom.SetMessages([]agent.AgentMessage{{Custom: map[string]any{"role": "custom", "text": "Hook content", "timestamp": int64(1)}}})
	if err := custom.Continue(ctx); err != nil {
		panic(err)
	}
	data, err := json.Marshal(convertedText)
	if err != nil {
		panic(err)
	}
	fmt.Printf("AGENT_STREAM converter %s\n", data)
}

// newAgent builds an agent; one built with neither a stream function nor a configured default gets a stream that is never called, as the Pi counterpart supplies one.
func newAgent(opts agent.AgentOptions) *agent.Agent {
	if opts.StreamFn == nil && opts.DefaultStreamFn == nil {
		if _, err := agent.GetDefaultStreamFn(); err != nil {
			opts.StreamFn = func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return nil, errors.New("stream function not expected")
			}
		}
	}
	a, err := agent.NewAgent(opts)
	if err != nil {
		panic(err)
	}
	return a
}
