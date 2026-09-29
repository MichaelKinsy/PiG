package testfixture

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func providerCancelRequest(content any) bool {
	if content == "cancel" {
		return true
	}
	blocks, ok := content.([]any)
	if !ok || len(blocks) == 0 {
		return false
	}
	block, _ := blocks[0].(map[string]any)
	return block["text"] == "cancel"
}

// ProviderProducer is shared by isolated, packed and fused Go provider-stream tests.
func ProviderProducer() *sdk.Extension {
	ext := sdk.New("provider-producer")
	ext.RegisterProvider("extension-provider", sdk.ProviderConfig{
		"api": "issue-8964-extension-api", "baseUrl": "https://extension.invalid", "apiKey": "extension-key",
		"models": []any{map[string]any{"id": "faux", "name": "Faux", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096}},
		"streamSimple": func(ctx sdk.Context, model, request, options map[string]any) (*sdk.ModelEventStream, error) {
			if options["apiKey"] != "extension-key" {
				return nil, fmt.Errorf("wrong key: %v", options["apiKey"])
			}
			if messages, ok := request["messages"].([]any); ok && len(messages) > 0 {
				if message, ok := messages[len(messages)-1].(map[string]any); ok && providerCancelRequest(message["content"]) {
					if _, err := ctx.Exec("provider-started", nil); err != nil {
						return nil, err
					}
					<-ctx.Done()
					return nil, fmt.Errorf("provider stream aborted")
				}
			}
			message := map[string]any{"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": []any{map[string]any{"type": "text", "text": "custom provider response"}}, "stopReason": "stop", "timestamp": 1, "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
			stream := sdk.CreateAssistantMessageEventStream()
			stream.Push(map[string]any{"type": "start", "partial": message})
			stream.Push(map[string]any{"type": "text_start", "contentIndex": 0, "partial": message})
			stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "custom provider response", "partial": message})
			stream.Push(map[string]any{"type": "text_end", "contentIndex": 0, "content": "custom provider response", "partial": message})
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
			return stream, nil
		},
	})
	return ext
}
