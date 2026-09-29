package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir, err := os.MkdirTemp("", "pig-abort-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()
	services, err := coding.NewServices(coding.ServicesOptions{AgentDir: dir, CWD: dir})
	if err != nil {
		return err
	}
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ignored")}, StopReason: "stop"})})
	model := &ai.Model{ID: "faux-1", Provider: provider}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello, how are you?")}}}
	aborted := services.ModelRuntime().Complete(ctx, model, request, ai.StreamOptions{})
	request.Messages = append(request.Messages, *aborted, ai.UserMessage{Content: ai.UserText("What is 2 + 2?")})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
		messages := request.Messages()
		if len(messages) == 3 {
			if previous, ok := messages[1].(ai.AssistantMessage); ok && previous.StopReason == ai.StopReasonAborted && len(previous.Content) == 0 {
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("4")}, StopReason: "stop"}, nil
			}
		}
		return ai.FauxResponse{StopReason: "error", ErrorMessage: "missing aborted history"}, nil
	})})
	follow := services.ModelRuntime().Complete(context.Background(), model, request, ai.StreamOptions{})
	text := ""
	if len(follow.Content) > 0 {
		if block, ok := follow.Content[0].(ai.TextContent); ok {
			text = block.Text
		}
	}
	paced := ai.NewFauxProvider(ai.FauxConfig{TokensPerSecond: 100, MinTokenSize: 1, MaxTokenSize: 1})
	paced.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(strings.Repeat("abcdefghijklmnopqrstuvwxyz", 4))}, StopReason: "stop"})})
	midContext, stop := context.WithCancel(context.Background())
	defer stop()
	midStream := services.ModelRuntime().Stream(midContext, &ai.Model{ID: "faux-1", Provider: paced}, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("List names")}}}, ai.StreamOptions{})
	textLength := 0
	for event := range midStream.Events(context.Background()) {
		if delta, ok := event.(ai.TextDeltaEvent); ok {
			textLength += len(delta.Delta)
		}
		if textLength >= 50 {
			stop()
		}
	}
	mid := midStream.Result()
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"aborted": aborted.StopReason, "content": len(aborted.Content), "followUp": follow.StopReason, "midAborted": mid.StopReason, "midContent": len(mid.Content) > 0, "text": text})
}
