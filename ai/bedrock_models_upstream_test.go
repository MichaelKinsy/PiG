package ai

import "testing"

func TestBedrockUpstreamModelCatalog(t *testing.T) {
	models := ListModels("amazon-bedrock")
	// packages/ai/test/bedrock-models.test.ts:27
	t.Run("should get all available Bedrock models", func(t *testing.T) {
		if len(models) == 0 {
			t.Fatal("empty Bedrock catalog")
		}
	})
	// packages/ai/test/bedrock-models.test.ts:32
	t.Run("exposes Claude Opus 5 through an inference profile only", func(t *testing.T) {
		global, plain := false, false
		for _, model := range models {
			global = global || model.ID == "global.anthropic.claude-opus-5"
			plain = plain || model.ID == "anthropic.claude-opus-5"
		}
		if !global || plain {
			t.Fatalf("global inference profile=%t plain model=%t", global, plain)
		}
	})
}
