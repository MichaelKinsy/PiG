package codingagent

// DefaultThinkingLevel mirrors upstream's defaults.ts:
//
//	export const DEFAULT_THINKING_LEVEL: ThinkingLevel = "medium";
//
// Used as the fallback when settings.json does not define
// defaultThinkingLevel. Thinking-level initialization consumes it.
//
// If upstream ever changes the default, the tripwire test in
// defaults_test.go fails, surfacing the drift on the next sync.
const DefaultThinkingLevel = "medium"
