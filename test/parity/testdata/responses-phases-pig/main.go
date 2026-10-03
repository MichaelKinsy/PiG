package main

import (
	"context"
	"fmt"
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
	for _, tc := range []struct{ first, last, status string }{{"commentary", "commentary", "completed"}, {"final_answer", "final_answer", "completed"}, {"commentary", "final_answer", "completed"}, {"final_answer", "final_answer", "incomplete"}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_phase\",\"content\":[],\"phase\":%q}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_phase\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}],\"phase\":%q}}\n\ndata: {\"type\":%q,\"response\":{\"id\":\"resp_phase\",\"status\":%q,\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n", tc.first, tc.last, "response."+tc.status, tc.status)
		}))
		provider := ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{Model: "gpt-5-mini", ProviderID: "openai", APIKey: "test", BaseURL: server.URL})
		// Pi's oracle reads each partial at publication. The consumer must attach in the producer's synchronous prefix: a goroutine outside the continuation executor can attach after the buffered response has published every event and then reads the later stop reason. RunStreamContinuation owns that prefix through iteration, as the Agent does (agent-loop.ts:402-414).
		ctx := ai.WithStreamContinuations(context.Background())
		var stream *ai.AssistantMessageEventStream
		line := fmt.Sprintf("%s/%s/%s", tc.first, tc.last, tc.status)
		if err := ai.RunStreamContinuation(ctx, func(observation *ai.StreamObservation) error {
			var err error
			stream, err = provider.Stream(observation.Context(ctx), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{})
			if err != nil {
				return err
			}
			for event := range stream.Events(observation.Context(ctx)) {
				switch event := event.(type) {
				case ai.TextStartEvent:
					line += fmt.Sprintf(" start:%s", event.Partial.Observe().StopReason)
				case ai.TextEndEvent:
					line += fmt.Sprintf(" end:%s", event.Partial.Observe().StopReason)
				}
			}
			return nil
		}); err != nil {
			server.Close()
			return err
		}
		fmt.Printf("%s terminal:%s\n", line, stream.Result().StopReason)
		server.Close()
		if err := provider.Close(); err != nil {
			return err
		}
	}
	return nil
}
