// Package providers holds every built-in provider catalog: one file per provider of packages/ai/catalog/providers, registered in
// All in the order of providers/index.ts.
package providers

// Ports packages/ai/catalog/providers/index.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

// All returns every built-in provider definition. Add new providers here.
func All() []ProviderDefinition {
	return []ProviderDefinition{
		amazonBedrock(),
		antLing(),
		anthropic(),
		azure(),
		baseten(),
		cerebras(),
		cloudflareAiGateway(),
		cloudflareWorkersAi(),
		deepseek(),
		fireworks(),
		githubCopilot(),
		googleVertex(),
		google(),
		groq(),
		huggingface(),
		kimiCoding(),
		meta(),
		minimaxCn(),
		minimax(),
		mistral(),
		moonshotaiCn(),
		moonshotai(),
		nvidia(),
		openaiCodex(),
		openai(),
		opencodeGo(),
		opencode(),
		openrouter(),
		qwenTokenPlanCn(),
		qwenTokenPlanIndividual(),
		qwenTokenPlan(),
		radius(),
		together(),
		typesafe(),
		vercelAiGateway(),
		xai(),
		xiaomiTokenPlanAms(),
		xiaomiTokenPlanCn(),
		xiaomiTokenPlanSgp(),
		xiaomi(),
		zaiCodingCn(),
		zai(),
	}
}
