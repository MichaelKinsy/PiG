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
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{})
		if err != nil {
			server.Close()
			return err
		}
		fmt.Printf("%s/%s/%s", tc.first, tc.last, tc.status)
		for event := range stream.Events(context.Background()) {
			switch event := event.(type) {
			case ai.TextStartEvent:
				fmt.Printf(" start:%s", event.Partial.Observe().StopReason)
			case ai.TextEndEvent:
				fmt.Printf(" end:%s", event.Partial.Observe().StopReason)
			}
		}
		fmt.Printf(" terminal:%s\n", stream.Result().StopReason)
		server.Close()
		if err := provider.Close(); err != nil {
			return err
		}
	}
	return nil
}
