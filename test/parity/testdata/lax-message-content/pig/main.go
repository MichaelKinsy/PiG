package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	messages := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{}, ai.AssistantMessage{API: ai.APIOpenAICompletions, Provider: "openai", Model: "test-model", StopReason: ai.StopReasonStop}, ai.ToolResultMessage{ToolCallID: "call_1", ToolName: "web_search"}}}).Messages()
	contents := []json.RawMessage{}
	for _, message := range messages {
		raw, err := json.Marshal(message)
		if err != nil {
			panic(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			panic(err)
		}
		contents = append(contents, object["content"])
	}
	if err := json.NewEncoder(os.Stdout).Encode(contents); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
