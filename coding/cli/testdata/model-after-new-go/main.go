// Command model-after-new-go is the Go SDK realization of testdata/model-after-new.mjs: it registers an extension provider whose models carry metadata no default produces (a window, an output limit, costs, reasoning and an image input) over the server PIG_TEST_MODEL_AFTER_NEW_URL names, and records the model each model_select event carries and the thinking level the extension reads then, as lines of PIG_TEST_REPLACE_LOG. Every Session start runs the factory again in a fresh process.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func record(line map[string]any) {
	data, err := json.Marshal(line)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	f, err := os.OpenFile(os.Getenv("PIG_TEST_REPLACE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", data)
}

// describe is the model the way the other fixtures log it; a provider reaches the extension as its id or as an object carrying it.
func describe(model map[string]any) map[string]any {
	provider := model["provider"]
	if object, ok := provider.(map[string]any); ok {
		provider = object["id"]
	}
	return map[string]any{
		"id": fmt.Sprintf("%v/%v", provider, model["id"]), "name": model["name"], "contextWindow": model["contextWindow"], "maxTokens": model["maxTokens"],
		"reasoning": model["reasoning"], "input": model["input"], "cost": model["cost"],
	}
}

func main() {
	ext := sdk.New("model-after-new-go")
	cost := map[string]any{"input": 3.25, "output": 16.5, "cacheRead": 0.325, "cacheWrite": 4.0625}
	ext.RegisterProvider("metaprov", sdk.ProviderConfig{
		"api": "openai-completions", "baseUrl": os.Getenv("PIG_TEST_MODEL_AFTER_NEW_URL"), "apiKey": "probe-key",
		"models": []any{
			map[string]any{"id": "model-a", "name": "Model A", "reasoning": true, "input": []string{"text", "image"}, "cost": cost, "contextWindow": 777000, "maxTokens": 55555},
			map[string]any{"id": "model-b", "name": "Model B", "reasoning": true, "input": []string{"text"}, "cost": cost, "contextWindow": 555000, "maxTokens": 44444},
		},
	})
	ext.OnEvent(sdk.EventSessionStart, func(_ sdk.Context, data map[string]any) (any, error) {
		record(map[string]any{"event": "session_start", "reason": data["reason"]})
		return nil, nil
	})
	ext.OnEvent(sdk.EventModelSelect, func(ctx sdk.Context, data map[string]any) (any, error) {
		thinking, err := ctx.GetThinkingLevel()
		if err != nil {
			thinking = "error: " + err.Error()
		}
		model, _ := data["model"].(map[string]any)
		record(map[string]any{"event": "model_select", "source": data["source"], "model": describe(model), "thinking": thinking})
		return nil, nil
	})
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "model-after-new-go: %v\n", err)
		os.Exit(1)
	}
}
