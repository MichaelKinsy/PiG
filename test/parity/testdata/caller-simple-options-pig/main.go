package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

type observation struct {
	MaxTokens int     `json:"maxTokens"`
	Reasoning *string `json:"reasoning"`
	TimeoutMs *int    `json:"timeoutMs"`
}

type provider struct{ calls []observation }

func (*provider) ID() string   { return "caller" }
func (*provider) Close() error { return nil }
func (p *provider) Stream(_ context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	var reasoning *string
	if options.Thinking != "" {
		reasoning = new(string(options.Thinking))
	}
	p.calls = append(p.calls, observation{options.MaxTokens, reasoning, options.TimeoutMs})
	stream := ai.NewAssistantMessageEventStream()
	stream.End(&ai.AssistantMessage{Model: "caller", Provider: "caller", StopReason: ai.StopReasonStop})
	return stream, nil
}

func run() (err error) {
	dir, err := os.MkdirTemp("", "caller-simple-options-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		return err
	}
	callback := &provider{}
	model := &ai.Model{ID: "caller", Provider: callback, Capabilities: ai.ModelCapabilities{ContextWindow: 128, MaxOutputTokens: 16}}
	for _, maxTokens := range []int{0, 98765} {
		message := services.ModelRuntime().CompleteSimple(context.Background(), model, ai.Context{}, ai.StreamOptions{MaxTokens: maxTokens, TimeoutMs: new(0)})
		if message.StopReason != ai.StopReasonStop {
			return fmt.Errorf("caller request failed: %s", message.ErrorMessage)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(callback.calls)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
