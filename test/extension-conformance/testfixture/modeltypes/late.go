package modeltypes

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// lateConfig is the provider config the late_provider tool registers after the factory finished: Pi's pi.registerProvider takes the whole ProviderConfig, callbacks included, at any time (.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1875-1903, loader.ts:449-457, runner.ts:517-523).
func lateConfig() sdk.ProviderConfig {
	return sdk.ProviderConfig{
		"api": "late-chat-api", "baseUrl": "https://late.test/v1", "apiKey": "late-key",
		"models": []any{
			map[string]any{"id": "chat", "name": "Chat", "reasoning": false, "input": []string{"text"}, "cost": zeroCost, "contextWindow": 1000, "maxTokens": 100},
			map[string]any{"id": "flux", "name": "Flux", "type": "image", "api": "late-images", "input": []string{"text"}, "output": []string{"image"}, "cost": zeroCost},
			map[string]any{"id": "cls", "name": "Cls", "type": "classifier", "api": "late-classifier", "input": []string{"text"}, "contextWindow": 1000, "cost": zeroCost},
		},
		"images":      map[string]sdk.ProviderImagesFunc{"late-images": imageImpl},
		"classifiers": map[string]sdk.ProviderClassifyFunc{"late-classifier": classifyImpl},
		"streamSimple": sdk.ProviderStreamSimpleFunc(func(_ sdk.Context, model, _, options map[string]any) (*sdk.ModelEventStream, error) {
			message := map[string]any{"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("late:%v:%v", model["id"], options["apiKey"])}}, "stopReason": "stop", "timestamp": 1}
			stream := sdk.CreateAssistantMessageEventStream()
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
			return stream, nil
		}),
	}
}

func registerLateProbe(e *sdk.Extension) {
	e.RegisterTool(sdk.ToolDefinition{
		Name: "late_provider", Label: "late_provider", Description: "Registers a provider with operations after the factory finished.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			registry := ctx.ModelRegistry()
			if err := registry.RegisterProvider("late", lateConfig()); err != nil {
				return nil, err
			}
			switch args["mode"] {
			case "again":
				if err := registry.RegisterProvider("late", sdk.ProviderConfig{"baseUrl": "https://late.test/v2"}); err != nil {
					return nil, err
				}
			case "gone":
				if err := registry.UnregisterProvider("late"); err != nil {
					return nil, err
				}
			}
			return text(map[string]any{"mode": args["mode"]})
		},
	})
}
