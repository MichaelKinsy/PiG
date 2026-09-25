package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	for _, identity := range []struct{ name, fields string }{
		{"repeated", `"id":"fc_original","call_id":"call_original","name":"read",`},
		{"omitted", ""},
		{"different", `"id":"fc_final","call_id":"call_final","name":"other",`},
	} {
		if err := probe(identity.name, identity.fields); err != nil {
			return err
		}
	}
	return nil
}

func probe(name, fields string) error {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_original","call_id":"call_original","name":"read","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"main.go\"}"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call",%s"namespace":"functions","arguments":"{\"path\":\"final.go\"}"}}

data: {"type":"response.completed","response":{"status":"completed"}}

`, fields)
	}))
	defer server.Close()
	provider := ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "unused", Model: "probe", ProviderID: "openai"})
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("read")}}}), ai.StreamOptions{})
	if err != nil {
		return err
	}
	for event := range stream.Events(context.Background()) {
		switch event := event.(type) {
		case ai.ToolCallStartEvent:
			printCall(os.Stdout, name, "start", event.Partial.Content[event.ContentIndex].(ai.ToolCall))
		case ai.ToolCallDeltaEvent:
			printCall(os.Stdout, name, "delta", event.Partial.Content[event.ContentIndex].(ai.ToolCall))
		case ai.ToolCallEndEvent:
			printCall(os.Stdout, name, "end", event.ToolCall)
		}
	}
	result := stream.Result()
	if result.StopReason != ai.StopReasonToolUse || len(result.Content) != 1 {
		return fmt.Errorf("unexpected response: %+v", result)
	}
	call := result.Content[0].(ai.ToolCall)
	printCall(os.Stdout, name, "result", call)
	fmt.Printf("%s:final %s %s\n", name, call.Namespace, call.Arguments["path"])
	return nil
}

func printCall(out io.Writer, name, stage string, call ai.ToolCall) {
	_, _ = fmt.Fprintf(out, "%s:%s %s %s\n", name, stage, call.ID, call.Name)
}
