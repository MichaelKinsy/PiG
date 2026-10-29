package providers

// Ports packages/ai/catalog/providers/typesafe.ts

import (
	"errors"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

func typesafe() ProviderDefinition {
	return ProviderDefinition{
		ID: "typesafe",
		Classifiers: func(ctx *ProviderContext) ([]*Obj, error) {
			metadata, ok := ctx.Sources.ModelsDevDecisions["typesafe/jev-latest"]
			if !ok || metadata.Type != "decision" {
				if ctx.Strict {
					return nil, errors.New("models.dev did not return decision model typesafe/jev-latest")
				}
				return []*Obj{}, nil
			}
			contextWindow := metadata.ContextLimit
			if contextWindow == 0 {
				contextWindow = 64000
			}
			return []*Obj{
				O(
					"type", "classifier",
					"id", "jev-latest",
					"name", metadata.Name,
					"api", "typesafe-system-one",
					"provider", "typesafe",
					"baseUrl", "https://api.typesafe.ai/v1/",
					// models.dev lists text input only, but the API accepts image assets as an experimental
					// feature behind an opt-in header, which the typesafe-system-one API sends.
					// https://github.com/typesafe-ai/typesafe-sdk-js (v0.7.0 changelog)
					"input", Strings("text", "image"),
					// The canonical models.dev entry has no direct-provider pricing. System One reports token usage,
					// so classify() results carry token counts but price them at zero.
					"cost", O("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
					"contextWindow", contextWindow,
					"compat", O("imageInput", "assets", "sendImageOptInHeader", true),
				),
			}, nil
		},
	}
}
