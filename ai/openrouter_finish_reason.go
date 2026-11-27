package ai

import "github.com/MichaelKinsy/PiG/internal/jsstring"

// OpenRouter's `native_finish_reason`: the upstream provider's own finish reason, which OpenRouter
// sends next to the OpenAI-style `finish_reason` it normalizes it into. Recorded for diagnostics, and
// used to catch upstream failures that OpenRouter reports as an empty `stop`.
// Ports packages/ai/src/utils/openrouter-finish-reason.ts (Pi ddf5868d2, 9b911b409).

// readOpenRouterNativeFinishReason returns OpenRouter's `native_finish_reason` from a streamed
// chat completion choice when present, mirroring packages/ai/src/utils/openrouter-finish-reason.ts.
func readOpenRouterNativeFinishReason(choice *oaiChunkChoice) string {
	if choice == nil {
		return ""
	}
	return choice.NativeFinishReason
}

// applyOpenRouterNativeFinishReason records a native finish reason that differs from finishReason
// on a message whose stop reason was already mapped from finishReason: RawStopReason becomes e.g.
// "stop.MALFORMED_RESPONSE", and an error message gets the native reason appended.
func applyOpenRouterNativeFinishReason(message *AssistantMessage, finishReason, nativeReason string) {
	if nativeReason == "" || nativeReason == finishReason {
		return
	}
	message.RawStopReason = finishReason + "." + nativeReason
	if message.StopReason == StopReasonError && message.ErrorMessage != "" {
		// Appended, not replaced: retry matching relies on the finish_reason message prefix.
		message.ErrorMessage += " (native_finish_reason: " + nativeReason + ")"
	}
}

// openRouterCleanStopNativeReasons lists native reasons OpenRouter treats as a clean stop.
// Lowercase, compared case-insensitively, because upstreams differ in case ("STOP", "stop").
var openRouterCleanStopNativeReasons = map[string]struct{}{
	"stop":          {},
	"end_turn":      {},
	"stop_sequence": {},
	"eos":           {},
	"complete":      {},
	"completed":     {},
	"pause_turn":    {},
	"compaction":    {},
	"end_of_stream": {},
}

// flagOpenRouterEmptyStop turns a finished `stop` without visible output (text or tool calls;
// reasoning does not count) into an error when the native reason is not a clean stop. The message
// matches the retry pattern for OpenRouter's own "returned an empty response", so the agent retries it.
func flagOpenRouterEmptyStop(message *AssistantMessage, nativeReason string) {
	if message.StopReason != StopReasonStop || nativeReason == "" {
		return
	}
	if _, clean := openRouterCleanStopNativeReasons[jsstring.ToLower(nativeReason)]; clean {
		return
	}
	for _, block := range message.Content {
		switch b := block.(type) {
		case ToolCall:
			return
		case TextContent:
			if trimJSWhitespace(b.Text) != "" {
				return
			}
		}
	}
	message.StopReason = StopReasonError
	message.ErrorMessage = "OpenRouter returned an empty response (native_finish_reason: " + nativeReason + ")"
}
