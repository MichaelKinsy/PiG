# PortAi rows (fix/pi-ai-azure-toollimit; Pi ea1cfcc4c, 45344bd3a)

| Upstream file | Disposition | Go target | Covering tests | Evidence |
|---|---|---|---|---|
| packages/ai/catalog/providers/azure.ts | ported (cost kept with tiers) | internal/modelgen/providers/azure.go (azureOpenAIModels) | internal/modelgen/providers/azure_test.go TestAzureKeepsOpenAILongContextPriceTier | red 9b7b8cee686, green 9624fc45422 |
| packages/ai/test/azure-models.test.ts | ported | internal/modelgen/providers/azure_test.go | same | same |
| packages/ai/src/api/strict-tool-limits.ts | ported (new file) | ai/strict_tool_limits.go (isStrictToolLimitErrorMessage moved here from ai/constrained_sampling.go, as upstream; exported kit hooks MarkStrictToolLimitsExceeded/HasExceededStrictToolLimits in ai/provider_kit.go for the bedrock package) | ai/strict_tool_limit_upstream_test.go (sessions remembered until cleanup; cap 10,000 drops oldest) | |
| packages/ai/src/api/anthropic-messages.ts | ported | ai/anthropic.go (initial disablePreferredStrictTools from session; mark on fallback) | ai/anthropic_strict_tool_fallback_upstream_test.go | |
| packages/ai/src/api/bedrock-converse-stream.ts | ported | ai/providers/bedrock/bedrock.go | ai/providers/bedrock/bedrock_strict_tool_fallback_upstream_test.go | |
| packages/ai/src/api/constrained-sampling.ts | ported (isStrictToolLimitErrorMessage removed from it) | ai/constrained_sampling.go | ai/strict_tool_limit_upstream_test.go | |
| packages/ai/test/{anthropic,bedrock}-strict-tool-fallback.test.ts, strict-tool-limits.test.ts | ported | files above | files above | |

Notes: the tool-limit memory is Anthropic and Bedrock only (no OpenAI completions/responses path uses it in Pi). Data: ai/models_generated.go and cmd/gen-models goldens for the azure provider need PinBump's regeneration (tiers added to Azure gpt-5.4/gpt-6-* entries).
