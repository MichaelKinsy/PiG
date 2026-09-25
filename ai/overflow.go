package ai

import "regexp"

// overflowPatterns detect context overflow errors from different providers.
// Mirrors upstream OVERFLOW_PATTERNS (packages/ai/src/utils/overflow.ts).
var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt (?:is )?too long`),                                                                   // Anthropic and z.ai token overflow
	regexp.MustCompile(`(?i)request_too_large`),                                                                         // Anthropic request byte-size overflow (HTTP 413)
	regexp.MustCompile(`(?i)input is too long for requested model`),                                                     // Amazon Bedrock
	regexp.MustCompile(`(?i)exceeds the context window`),                                                                // OpenAI (Completions & Responses API)
	regexp.MustCompile(`(?i)exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`), // OpenAI-compatible proxies (LiteLLM)
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),                                                    // Google (Gemini)
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),                                                              // xAI (Grok)
	regexp.MustCompile(`(?i)reduce the length of the messages`),                                                         // Groq
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),                                                      // OpenRouter (most backends)
	regexp.MustCompile(`(?i)exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`),                           // OpenRouter/Poolside
	regexp.MustCompile(`(?i)input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`),            // Together AI
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),                                                                  // GitHub Copilot
	regexp.MustCompile(`(?i)exceeds the available context size`),                                                        // llama.cpp server
	regexp.MustCompile(`(?i)greater than the context length`),                                                           // LM Studio
	regexp.MustCompile(`(?i)context window exceeds limit`),                                                              // MiniMax
	regexp.MustCompile(`(?i)exceeded model token limit`),                                                                // Kimi For Coding
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`),                                       // Mistral
	regexp.MustCompile(`(?i)prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`),              // DS4 server
	regexp.MustCompile(`(?i)model_context_window_exceeded`),                                                             // z.ai non-standard finish_reason surfaced as error text
	regexp.MustCompile(`(?i)prompt too long; exceeded (?:max )?context length`),                                         // Ollama explicit overflow error
	regexp.MustCompile(`(?i)range of input length should be`),                                                           // DashScope / Qwen Token Plan
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),                                                             // Generic fallback
	regexp.MustCompile(`(?i)too many tokens`),                                                                           // Generic fallback
	regexp.MustCompile(`(?i)token limit exceeded`),                                                                      // Generic fallback
}

// cerebrasBodylessOverflowPattern is Cerebras's bodyless 400/413 overflow.
var cerebrasBodylessOverflowPattern = regexp.MustCompile(`(?i)^4(?:00|13)\s*(?:status code)?\s*\(no body\)`)

// nonOverflowPatterns exclude rate limiting and server errors that also match
// an overflow pattern, such as Bedrock's "ThrottlingException: Too many
// tokens". Mirrors upstream NON_OVERFLOW_PATTERNS.
var nonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(Throttling error|Service unavailable):`), // AWS Bedrock non-overflow errors
	regexp.MustCompile(`(?i)rate limit`),                               // Generic rate limiting
	regexp.MustCompile(`(?i)too many requests`),                        // Generic HTTP 429 style
}

func matchesAny(patterns []*regexp.Regexp, text string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// IsContextOverflow reports whether an assistant message represents a context
// overflow: an error whose message matches a provider overflow pattern, a
// successful response whose input exceeds contextWindow (z.ai silent
// overflow), or a length stop with no output that filled the context window
// (Xiaomi MiMo). contextWindow 0 disables the usage-based cases. Mirrors
// upstream isContextOverflow.
func IsContextOverflow(message AssistantMessage, contextWindow int) bool {
	if message.StopReason == StopReasonError && message.ErrorMessage != "" && !matchesAny(nonOverflowPatterns, message.ErrorMessage) {
		if matchesAny(overflowPatterns, message.ErrorMessage) {
			return true
		}
		if message.Provider == "cerebras" && cerebrasBodylessOverflowPattern.MatchString(message.ErrorMessage) {
			return true
		}
	}
	inputTokens := message.Usage.Input + message.Usage.CacheRead
	if contextWindow != 0 && message.StopReason == StopReasonStop && inputTokens > contextWindow {
		return true
	}
	return contextWindow != 0 && message.StopReason == StopReasonLength && message.Usage.Output == 0 &&
		float64(inputTokens) >= float64(contextWindow)*0.99
}

// IsRecoverableLength reports whether a length stop ended below the intended
// output limit. desiredMaxOutput must be the limit before any context-based
// clamping. Mirrors upstream isRecoverableLength.
func IsRecoverableLength(message AssistantMessage, desiredMaxOutput int) bool {
	return message.StopReason == StopReasonLength && desiredMaxOutput > 0 && message.Usage.Output < desiredMaxOutput
}

// GetOverflowPatterns returns a copy of the overflow patterns. Mirrors
// upstream getOverflowPatterns.
func GetOverflowPatterns() []*regexp.Regexp {
	return append([]*regexp.Regexp(nil), overflowPatterns...)
}
