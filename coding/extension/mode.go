package extension

// ExtensionMode is the run mode pi is operating in, exposed to
// extensions via Context.Mode(). Extensions guard terminal-only UI
// (custom components, dialogs) on mode == ModeTUI.
//
// upstream: packages/coding-agent/src/core/extensions/types.ts:298
// (`export type ExtensionMode = "tui" | "rpc" | "json" | "print"`)
type ExtensionMode string

const (
	// ModeTUI is the interactive terminal UI. Set by interactive mode
	// (interactive-mode.ts:1511 `mode: "tui"`).
	ModeTUI ExtensionMode = "tui"
	// ModeRPC is the headless JSONL command/event loop. Set by rpc mode
	// (rpc-mode.ts:320 `mode: "rpc"`).
	ModeRPC ExtensionMode = "rpc"
	// ModeJSON is print mode emitting all events as JSON. Set by print
	// mode when its output mode is "json" (print-mode.ts:74).
	ModeJSON ExtensionMode = "json"
	// ModePrint is print mode emitting only the final text response.
	// The upstream Runner default (runner.ts:229).
	ModePrint ExtensionMode = "print"
)

// normalize returns the mode, defaulting an unset (zero-value) mode to
// ModePrint to match upstream's `private mode: ExtensionMode = "print"`
// (runner.ts:229).
func (m ExtensionMode) normalize() ExtensionMode {
	if m == "" {
		return ModePrint
	}
	return m
}
