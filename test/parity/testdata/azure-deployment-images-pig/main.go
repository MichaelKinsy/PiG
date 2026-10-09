package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data, err := os.ReadFile("ai/testdata/upstream-red-circle.png")
	if err != nil {
		return err
	}
	type captured struct {
		Model  string
		Images int
	}
	requests := make(chan captured, 2)
	var turn atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
			Input []struct {
				Type   string          `json:"type"`
				Output json.RawMessage `json:"output"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		images := 0
		for _, item := range payload.Input {
			if item.Type == "function_call_output" {
				var parts []struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(item.Output, &parts) == nil {
					for _, part := range parts {
						if part.Type == "input_image" {
							images++
						}
					}
				}
			}
		}
		requests <- captured{payload.Model, images}
		w.Header().Set("Content-Type", "text/event-stream")
		if turn.Add(1) == 1 {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_circle\",\"call_id\":\"call_circle\",\"name\":\"image\",\"arguments\":\"\"}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_circle\",\"call_id\":\"call_circle\",\"name\":\"image\",\"arguments\":\"{}\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		} else {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		}
	}))
	defer server.Close()
	provider := ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{APIKey: "test", Model: "gpt-4o-mini", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Describe the image.")}}, Tools: []ai.ToolSchema{{Name: "image", Parameters: ai.JsonObject{"type": "object", "properties": ai.JsonObject{}}}}}
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(request), ai.StreamOptions{AzureDeploymentName: "image-deployment"})
	if err != nil {
		return err
	}
	first := stream.Result()
	if first.StopReason != ai.StopReasonToolUse {
		return fmt.Errorf("first=%+v", first)
	}
	call := first.Content[0].(ai.ToolCall)
	request.Messages = append(request.Messages, *first, ai.ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "A red circle."}, ai.ImageContent{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(data)}}})
	stream, err = provider.Stream(context.Background(), ai.NormalizeContext(request), ai.StreamOptions{AzureDeploymentName: "image-deployment"})
	if err != nil {
		return err
	}
	if last := stream.Result(); last.StopReason != ai.StopReasonStop {
		return fmt.Errorf("last=%+v", last)
	}
	one, two := <-requests, <-requests
	fmt.Printf("logical=%s api=%s requests=%s,%s images=%d\n", first.Model, first.API, one.Model, two.Model, two.Images)
	return nil
}
