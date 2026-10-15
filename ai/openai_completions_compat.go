package ai

import (
	"reflect"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// detectOpenAICompletionsCompat is openai-completions.ts detectCompat: the compat flags a provider name, base URL and model id imply.
func detectOpenAICompletionsCompat(providerID, baseURL, modelID string) OpenAICompat {
	has := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(baseURL, part) {
				return true
			}
		}
		return false
	}
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isZai := providerID == "zai" || providerID == "zai-coding-cn" || has("api.z.ai", "open.bigmodel.cn")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isTogether := providerID == "together" || has("api.together.ai", "api.together.xyz")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isMoonshot := providerID == "moonshotai" || providerID == "moonshotai-cn" || has("api.moonshot.")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isOpenRouter := providerID == "openrouter" || has("openrouter.ai")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isCloudflareWorkersAI := providerID == "cloudflare-workers-ai" || has("api.cloudflare.com")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isCloudflareAIGateway := providerID == "cloudflare-ai-gateway" || has("gateway.ai.cloudflare.com")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isNvidia := providerID == "nvidia" || has("integrate.api.nvidia.com")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isAntLing := providerID == "ant-ling" || has("api.ant-ling.com")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isCerebras := providerID == "cerebras" || has("cerebras.ai")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isDeepSeek := providerID == "deepseek" || strings.Contains(jsstring.ToLower(baseURL), "deepseek.com")
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isNonStandard := isNvidia || isCerebras || providerID == "xai" || has("api.x.ai") || isTogether || has("chutes.ai") || isDeepSeek || isZai || isMoonshot ||
		//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
		providerID == "opencode" || has("opencode.ai") || isCloudflareWorkersAI || isCloudflareAIGateway || isAntLing
	useMaxTokens := has("chutes.ai") || isDeepSeek || isMoonshot || isCloudflareAIGateway || isTogether || isNvidia || isAntLing || isZai
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	isGrok := providerID == "xai" || has("api.x.ai")
	isOpenRouterDeveloperRoleModel := isOpenRouter && (strings.HasPrefix(modelID, "anthropic/") || strings.HasPrefix(modelID, "openai/"))

	maxTokensField := "max_completion_tokens"
	if useMaxTokens {
		maxTokensField = "max_tokens"
	}
	thinkingFormat := "openai"
	switch {
	case isDeepSeek:
		thinkingFormat = "deepseek"
	case isZai:
		thinkingFormat = "zai"
	case isTogether:
		thinkingFormat = "together"
	case isAntLing:
		thinkingFormat = "ant-ling"
	case isOpenRouter:
		thinkingFormat = "openrouter"
	}
	cacheControlFormat := ""
	//portlint:allow providerliteral detectCompat special-cases these providers by name and base URL in Pi (openai-completions.ts:1593-1694)
	if providerID == "openrouter" && strings.HasPrefix(modelID, "anthropic/") {
		cacheControlFormat = "anthropic"
	}
	sessionAffinityFormat := SessionAffinityOpenAI
	if isOpenRouter {
		sessionAffinityFormat = SessionAffinityOpenRouter
	}
	return OpenAICompat{
		SupportsStore:                               new(!isNonStandard),
		SupportsDeveloperRole:                       new(isOpenRouterDeveloperRoleModel || (!isNonStandard && !isOpenRouter)),
		SupportsReasoningEffort:                     new(!isGrok && !isZai && !isMoonshot && !isTogether && !isCloudflareAIGateway && !isNvidia && !isAntLing),
		SupportsUsageInStreaming:                    new(true),
		SupportsFinishReason:                        new(true),
		MaxTokensField:                              maxTokensField,
		RequiresToolResultName:                      new(false),
		RequiresAssistantAfterToolResult:            new(false),
		RequiresThinkingAsText:                      new(false),
		RequiresReasoningContentOnAssistantMessages: new(isDeepSeek),
		ThinkingFormat:                              thinkingFormat,
		ZaiToolStream:                               new(false),
		SupportsThinkingTokenBudget:                 new(false),
		// OpenAI compatibility alone does not imply strict JSON-schema tool support.
		SupportsStrictMode:             new(false),
		SupportsOpenAIGrammarTools:     new(false),
		SupportsMidConvoSystemMessages: new(false),
		SupportsMidConvoToolAdditions:  new(false),
		CacheControlFormat:             cacheControlFormat,
		SendSessionAffinityHeaders:     new(isOpenRouter),
		SessionAffinityFormat:          sessionAffinityFormat,
		SupportsLongCacheRetention:     new(!isTogether && !isCloudflareWorkersAI && !isCloudflareAIGateway && !isNvidia && !isAntLing),
	}
}

// getOpenAICompletionsCompat is openai-completions.ts getCompat: the detected compat with each setting the model's own compat gives overlaid.
func getOpenAICompletionsCompat(providerID, baseURL, modelID string, model *OpenAICompat) *OpenAICompat {
	detected := detectOpenAICompletionsCompat(providerID, baseURL, modelID)
	if model == nil {
		return &detected
	}
	merged := reflect.ValueOf(&detected).Elem()
	overlay := reflect.ValueOf(*model)
	for i := range overlay.NumField() {
		if field := merged.Type().Field(i); field.IsExported() && !overlay.Field(i).IsZero() {
			merged.Field(i).Set(overlay.Field(i))
		}
	}
	return &detected
}
