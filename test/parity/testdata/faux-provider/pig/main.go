package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	provider := ai.NewFauxProvider(ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
	canonicalModel := provider.GetModel()
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxThinking("go"), ai.FauxText("ok"), ai.FauxToolCall("echo", map[string]any{}, &ai.FauxToolCallOptions{ID: "tool-1"})}, StopReason: "toolUse"}), ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ready")}, StopReason: "stop"})})
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}
	firstStream, err := provider.Stream(context.Background(), ai.NormalizeContext(request), ai.StreamOptions{SessionID: "session-1", CacheRetention: ai.CacheRetentionShort})
	if err != nil {
		return err
	}
	types := []ai.AssistantEventType{}
	for event := range firstStream.Events(context.Background()) {
		types = append(types, event.EventType())
	}
	first := firstStream.Result()
	request.Messages = append(request.Messages, *first, ai.ToolResultMessage{ToolCallID: "tool-1", ToolName: "echo", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, ai.UserMessage{Content: ai.UserText("follow up")})
	secondStream, err := provider.Stream(context.Background(), ai.NormalizeContext(request), ai.StreamOptions{SessionID: "session-1", CacheRetention: ai.CacheRetentionShort})
	if err != nil {
		return err
	}
	second := secondStream.Result()
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
		return ai.FauxResponse{}.AssistantMessage(), errors.New("boom")
	})})
	failedStream, err := provider.Stream(context.Background(), ai.NormalizeContext(request), ai.StreamOptions{})
	if err != nil {
		return err
	}
	failed := failedStream.Result()
	failedTypes := []ai.AssistantEventType{}
	for event := range failedStream.Events(context.Background()) {
		failedTypes = append(failedTypes, event.EventType())
	}
	paced := ai.NewFauxProvider(ai.FauxConfig{TokensPerSecond: 100, TokenSize: &ai.FauxTokenSize{Min: new(3), Max: new(3)}})
	paced.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("abcdefghijklmnopqrstuvwxyz")}, StopReason: "stop"})})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aborting, err := paced.Stream(ctx, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{})
	if err != nil {
		return err
	}
	abortEvents := []ai.AssistantEventType{}
	for event := range aborting.Events(context.Background()) {
		abortEvents = append(abortEvents, event.EventType())
		if event.EventType() == ai.EventTextDelta {
			cancel()
		}
	}
	counters := func(u ai.Usage) map[string]int {
		return map[string]int{"input": u.Input, "output": u.Output, "cacheRead": u.CacheRead, "cacheWrite": u.CacheWrite, "totalTokens": u.TotalTokens}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"abortEvents": abortEvents, "events": types, "firstUsage": counters(first.Usage), "secondUsage": counters(second.Usage), "error": failed.ErrorMessage, "errorEvents": failedTypes, "calls": provider.CallCount(), "canonicalIdentity": canonicalModel == provider.GetModel(), "canonicalProvider": canonicalModel.ProviderMeta.ProviderID})
}
