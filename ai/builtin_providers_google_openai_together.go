package ai

// Ports packages/ai/src/providers/{google,openai,together}.ts: the provider functions of the Google, OpenAI and Together
// providers. Each is the built-in provider assembled by builtinProvider from its model catalog and registered chat API.

// GoogleProvider is the Google provider (providers/google.ts googleProvider).
func GoogleProvider() *ModelsProvider { return builtinProvider("google") }

// OpenAIProvider is the OpenAI provider (providers/openai.ts openaiProvider).
func OpenAIProvider() *ModelsProvider { return builtinProvider("openai") }

// TogetherProvider is the Together provider (providers/together.ts togetherProvider).
func TogetherProvider() *ModelsProvider { return builtinProvider("together") }
