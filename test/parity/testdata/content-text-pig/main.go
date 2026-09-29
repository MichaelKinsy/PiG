package main

import (
	"encoding/json"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	content := []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "reasoning"}, ai.TextContent{Text: "first"}, ai.ToolCall{ID: "1", Name: "read", Arguments: ai.JsonObject{}}, ai.TextContent{Text: "second"}}
	result := []ai.ToolResultMessageContent{ai.TextContent{Text: "first"}, ai.ImageContent{Data: "...", MimeType: "image/png"}, ai.TextContent{Text: "second"}}
	if err := json.NewEncoder(os.Stdout).Encode([]string{ai.ContentText(content), ai.ContentText(content, ""), ai.ContentText("hello"), ai.ContentText(result, "")}); err != nil {
		panic(err)
	}
}
