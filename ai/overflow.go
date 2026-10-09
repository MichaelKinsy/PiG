package ai

import (
	"regexp"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// jsSpaceClass is the JavaScript \s class; Go's \s is ASCII-only and lacks \v and the Unicode spaces.
const jsSpaceClass = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

// jsDotClass is the JavaScript "." without the s flag; Go's "." still matches \r, U+2028 and U+2029.
const jsDotClass = `[^\n\r\x{2028}\x{2029}]`

// overflowPatterns detect context overflow errors from different providers.
// Mirrors upstream OVERFLOW_PATTERNS (packages/ai/src/utils/overflow.ts).
var overflowPatterns = []*lazyregexp.Regexp{
	lazyregexp.NewJSIgnoreCase(`prompt (?:is )?too long`),                                                                                     // Anthropic and z.ai token overflow
	lazyregexp.NewJSIgnoreCase(`prompt exceeds max length`),                                                                                   // z.ai CN endpoint token overflow
	lazyregexp.NewJSIgnoreCase(`request_too_large`),                                                                                           // Anthropic request byte-size overflow (HTTP 413)
	lazyregexp.NewJSIgnoreCase(`input is too long for requested model`),                                                                       // Amazon Bedrock
	lazyregexp.NewJSIgnoreCase(`exceeds the context window`),                                                                                  // OpenAI (Completions & Responses API)
	lazyregexp.NewJSIgnoreCase(`exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|` + jsSpaceClass + `*\([\d,]+\))`), // OpenAI-compatible proxies (LiteLLM)
	lazyregexp.NewJSIgnoreCase(`input token count` + jsDotClass + `*exceeds the maximum`),                                                     // Google (Gemini)
	lazyregexp.NewJSIgnoreCase(`maximum prompt length is \d+`),                                                                                // xAI (Grok)
	lazyregexp.NewJSIgnoreCase(`reduce the length of the messages`),                                                                           // Groq
	lazyregexp.NewJSIgnoreCase(`maximum context length is \d+ tokens`),                                                                        // OpenRouter (most backends)
	lazyregexp.NewJSIgnoreCase(`exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`),                                             // OpenRouter/Poolside
	lazyregexp.NewJSIgnoreCase(`input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`),                              // Together AI
	lazyregexp.NewJSIgnoreCase(`exceeds the limit of \d+`),                                                                                    // GitHub Copilot
	lazyregexp.NewJSIgnoreCase(`exceeds the available context size`),                                                                          // llama.cpp server
	lazyregexp.NewJSIgnoreCase(`greater than the context length`),                                                                             // LM Studio
	lazyregexp.NewJSIgnoreCase(`context window exceeds limit`),                                                                                // MiniMax
	lazyregexp.NewJSIgnoreCase(`exceeded model token limit`),                                                                                  // Kimi For Coding
	lazyregexp.NewJSIgnoreCase(`too large for model with \d+ maximum context length`),                                                         // Mistral
	lazyregexp.NewJSIgnoreCase(`prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`),                                // DS4 server
	lazyregexp.NewJSIgnoreCase(`model_context_window_exceeded`),                                                                               // z.ai non-standard finish_reason surfaced as error text
	lazyregexp.NewJSIgnoreCase(`prompt too long; exceeded (?:max )?context length`),                                                           // Ollama explicit overflow error
	lazyregexp.NewJSIgnoreCase(`range of input length should be`),                                                                             // DashScope / Qwen Token Plan
	lazyregexp.NewJSIgnoreCase(`context[_ ]length[_ ]exceeded`),                                                                               // Generic fallback
	lazyregexp.NewJSIgnoreCase(`too many tokens`),                                                                                             // Generic fallback
	lazyregexp.NewJSIgnoreCase(`token limit exceeded`),                                                                                        // Generic fallback
}

// cerebrasBodylessOverflowPattern is Cerebras's bodyless 400/413 overflow.
var cerebrasBodylessOverflowPattern = lazyregexp.NewJSIgnoreCase(`^4(?:00|13)` + jsSpaceClass + `*(?:status code)?` + jsSpaceClass + `*\(no body\)`)

// nonOverflowPatterns exclude rate limiting and server errors that also match
// an overflow pattern, such as Bedrock's "ThrottlingException: Too many
// tokens". Mirrors upstream NON_OVERFLOW_PATTERNS.
var nonOverflowPatterns = []*lazyregexp.Regexp{
	lazyregexp.NewJSIgnoreCase(`^(Throttling error|Service unavailable):`), // AWS Bedrock non-overflow errors
	lazyregexp.NewJSIgnoreCase(`rate limit`),                               // Generic rate limiting
	lazyregexp.NewJSIgnoreCase(`too many requests`),                        // Generic HTTP 429 style
}

func matchesAny(patterns []*lazyregexp.Regexp, text string) bool {
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
	patterns := make([]*regexp.Regexp, len(overflowPatterns))
	for i, pattern := range overflowPatterns {
		patterns[i] = pattern.Regexp()
	}
	return patterns
}
