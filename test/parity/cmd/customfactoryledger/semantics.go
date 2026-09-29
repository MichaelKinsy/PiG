package main

import "strings"

// This file is intentionally source-reviewed rather than inferred from names.
// Adding an inventory member without a semantic row makes generation fail.

type reviewedSemantic struct {
	timing      string
	behavior    string
	error       string
	disposition string
}

func reviewedMember(root, id string) (reviewedSemantic, bool) {
	property := memberProperty(id)
	if property == "" {
		if isConstruct(id) {
			switch root {
			case "pkg:coding-agent/.#Theme":
				return reviewedSemantic{"synchronous-construction", "constructs one Theme from complete foreground/background domains, color mode, and optional provenance", "invalid color values or domains throw during local construction", "public-live-proxy-realization-deferred-to-atomic-contract"}, true
			case "pkg:coding-agent/.#KeybindingsManager":
				return reviewedSemantic{"synchronous-construction", "constructs one manager from optional user bindings and config path, resolving defaults immediately", "invalid configuration is handled by the pinned manager load path", "public-replicated-manager-realization-deferred-to-atomic-contract"}, true
			}
		}
		return reviewedRoot(root)
	}

	var behavior string
	switch root {
	case "pkg:tui/.#Component":
		behavior = map[string]string{
			"render":          "calls component rendering synchronously with resolved content-cell width and returns ordered terminal lines without trailing newlines",
			"handleInput":     "optional synchronous handler receives each eligible raw input chunk in order",
			"wantsKeyRelease": "optional boolean opts into Kitty release delivery; omission is false",
			"invalidate":      "synchronously discards component-local cached rendering state",
			"handleMouse":     "optional synchronous handler receives each normalized mouse event with component-relative coordinates; undefined or a result claiming none of handled, capture, or focus passes the event on",
		}[property]
	case "pkg:tui/.#Focusable":
		behavior = map[string]string{"focused": "mutable strict component identity flag; the TUI sets it on focus transitions and the component uses it to emit APC cursor markers"}[property]
	case "pkg:tui/.#OverlayOptions":
		behavior = map[string]string{
			"width": "optional absolute cells or terminal-width percentage; default is min(80, available width)", "minWidth": "optional absolute lower width bound applied before available-space clamp", "maxHeight": "optional absolute rows or terminal-height percentage; clips complete trailing lines",
			"anchor": "optional one of nine anchors; omission selects center", "offsetX": "optional signed horizontal cell offset applied after anchor or explicit column", "offsetY": "optional signed vertical row offset applied after anchor or explicit row", "row": "optional absolute row or percentage of available travel", "col": "optional absolute column or percentage of available travel",
			"margin": "optional scalar or per-edge nonnegative terminal margin; negative values clamp to zero", "visible": "optional executable predicate evaluated with current terminal width and height each visibility cycle", "nonCapturing": "optional boolean excludes the entry from automatic focus while preserving explicit focus",
		}[property]
	case "pkg:tui/.#OverlayMargin":
		behavior = map[string]string{"top": "optional top-row margin", "right": "optional right-cell margin", "bottom": "optional bottom-row margin", "left": "optional left-cell margin"}[property]
	case "pkg:tui/.#OverlayUnfocusOptions":
		behavior = map[string]string{"target": "required explicit Component or null focus target used after this overlay releases focus"}[property]
	case "pkg:tui/.#OverlayHandle":
		behavior = map[string]string{
			"hide": "permanently removes this identified mounted entry; repeated and post-removal calls do nothing", "setHidden": "synchronously changes reversible hidden state; unchanged and post-removal calls do nothing", "isHidden": "synchronously returns this mounted entry's hidden flag and false after removal",
			"focus": "if mounted and visible, increments visual focus order and focuses this exact entry without changing append order", "unfocus": "releases this exact entry to an explicit target or the frontmost visible capturing fallback/pre-focus target", "isFocused": "synchronously compares strict entry identity with current focus",
			"getBounds": "synchronously returns a copy of the most recent rendered bounds while the entry is mounted and visible, otherwise undefined",
		}[property]
	case "pkg:tui/.#Terminal":
		behavior = terminalBehavior[property]
	case "pkg:tui/.#TUI":
		behavior = tuiBehavior[property]
	case "pkg:coding-agent/.#Theme":
		behavior = themeBehavior[property]
	case "pkg:coding-agent/.#KeybindingsManager":
		behavior = keybindingBehavior[property]
	case "pkg:coding-agent/.#ExtensionUIContext::property:custom":
		if property == "custom" {
			behavior = "awaits factory(tui, theme, keybindings, done), presents the returned component, and settles after the first completion or factory error"
		}
	}
	if behavior == "" {
		behavior = reviewedSupportBehavior(root, property)
	}
	if behavior == "" {
		return reviewedSemantic{}, false
	}
	return reviewedSemantic{
		timing:      memberTiming(root, property),
		behavior:    behavior,
		error:       memberError(root, property),
		disposition: memberDisposition(root, property),
	}, true
}

func reviewedRoot(root string) (reviewedSemantic, bool) {
	behavior := map[string]string{
		"pkg:coding-agent/.#SourceInfo": "optional Theme source provenance record", "pkg:tui/.#KeyId": "normalized key identifier union", "pkg:tui/.#Keybinding": "single key or ordered key-list binding", "pkg:tui/.#KeybindingConflict": "key and conflicting action bindings record", "pkg:tui/.#KeybindingDefinition": "action description and default keys record", "pkg:tui/.#Keybindings": "complete named TUI action-to-binding map", "pkg:tui/.#KeybindingsConfig": "partial named TUI action override map", "pkg:tui/.#RgbColor": "terminal RGB color record", "pkg:tui/.#TerminalColorScheme": "closed light/dark terminal scheme union", "pkg:tui/.#TuiStopOptions": "terminal stop presentation options",
		"pkg:ai/.#ThinkingLevel": "closed model reasoning-effort union consumed by Theme.getThinkingBorderColor", "pkg:tui/.#TuiMode": "closed regular/fullscreen renderer-mode union",
		"pkg:tui/.#TUI": "exported factory TUI object surface shared by regular and fullscreen implementations", "pkg:tui/.#Terminal": "exported terminal ownership and I/O surface", "pkg:tui/.#Component": "render/invalidate contract with optional ordered input and key-release opt-in", "pkg:tui/.#Focusable": "mutable strict focus identity contract",
		"pkg:tui/.#OverlayAnchor": "closed nine-value anchor union", "pkg:tui/.#OverlayMargin": "per-edge optional margin object", "pkg:tui/.#OverlayOptions": "static geometry, executable visibility, and capture options", "pkg:tui/.#OverlayHandle": "identity-targeted removal, hidden, focus, and query operations", "pkg:tui/.#OverlayUnfocusOptions": "optional explicit unfocus target wrapper", "pkg:tui/.#SizeValue": "absolute number or decimal percentage template literal",
		"pkg:tui/.#TuiInputListener": "ordered input transform/consume callback",
		"pkg:tui/.#TuiMouseEvent":    "normalized zero-based cell mouse event with component-local and absolute coordinates", "pkg:tui/.#TuiMouseEventResult": "optional handled, capture, focus, and render claims returned by a mouse handler", "pkg:tui/.#TuiMouseEventType": "closed press/release/move/drag/click/wheel union", "pkg:tui/.#TuiMouseButton": "closed left/middle/right/none union", "pkg:tui/.#OverlayBounds": "rendered overlay row, column, width, and height record", "pkg:tui/.#TuiInputListenerResult": "optional consume flag and replacement input result", "pkg:coding-agent/.#Theme": "stable live styling object with foreground/background domains and attributes", "pkg:coding-agent/.#ThemeColor": "closed foreground color-name union", "pkg:coding-agent/.#KeybindingsManager": "configured ordered action-to-key manager", "pkg:coding-agent/.#ExtensionUIContext::property:custom": "generic custom factory call surface",
	}[root]
	if behavior == "" {
		return reviewedSemantic{}, false
	}
	return reviewedSemantic{"type-shape", behavior, "type declaration has no runtime error channel", memberDisposition(root, "")}, true
}

var reviewedSupportProperties = map[string]map[string]string{
	"pkg:coding-agent/.#SourceInfo":  {"baseDir": "optional resolved source base directory", "origin": "source origin classification", "path": "optional source path", "scope": "source scope classification", "source": "source identifier"},
	"pkg:tui/.#KeybindingConflict":   {"key": "conflicting normalized key identifier", "keybindings": "ordered actions bound to the same key"},
	"pkg:tui/.#KeybindingDefinition": {"defaultKeys": "ordered default normalized key identifiers", "description": "human-readable action description"},
	"pkg:tui/.#RgbColor":             {"r": "red channel", "g": "green channel", "b": "blue channel"},
	"pkg:tui/.#TuiStopOptions":       {"preserveScreen": "optional request to preserve the rendered screen while stopping"},
	"pkg:tui/.#OverlayBounds":        {"row": "zero-based rendered top row", "col": "zero-based rendered left column", "width": "rendered width in cells", "height": "rendered height in rows"},
	"pkg:tui/.#TuiMouseEvent": {
		"type": "event type", "button": "button, none for motion without a pressed button", "x": "zero-based column local to the receiving component", "y": "zero-based row local to the receiving component",
		"screenX": "zero-based absolute terminal column", "screenY": "zero-based absolute terminal row", "width": "receiving component width", "height": "receiving component height",
		"shift": "shift modifier state", "alt": "alt modifier state", "ctrl": "ctrl modifier state", "wheelDelta": "optional wheel movement in logical lines; negative scrolls up", "clickCount": "optional consecutive click count for click events",
	},
	"pkg:tui/.#TuiMouseEventResult": {
		"handled": "optional claim that stops propagation and suppresses renderer fallback", "capture": "optional claim that routes later drag and release events to this component; implies handled",
		"focus": "optional claim that gives this component keyboard focus; implies handled", "render": "optional render request; move and release default to false, press, click, drag, and wheel default to true",
	},
}

var reviewedKeybindingsProperties = func() map[string]string {
	properties := `tui.altScreen.bottom tui.altScreen.halfPageDown tui.altScreen.halfPageUp tui.altScreen.lineDown tui.altScreen.lineUp tui.altScreen.nextPrompt tui.altScreen.pageDown tui.altScreen.pageUp tui.altScreen.previousPrompt tui.altScreen.search tui.altScreen.searchClose tui.altScreen.searchNext tui.altScreen.searchPrevious tui.altScreen.top tui.editor.cursorDown tui.editor.cursorLeft tui.editor.cursorLineEnd tui.editor.cursorLineStart tui.editor.cursorRight tui.editor.cursorUp tui.editor.cursorWordLeft tui.editor.cursorWordRight tui.editor.deleteCharBackward tui.editor.deleteCharForward tui.editor.deleteToLineEnd tui.editor.deleteToLineStart tui.editor.deleteWordBackward tui.editor.deleteWordForward tui.editor.historyNext tui.editor.historyPrevious tui.editor.jumpBackward tui.editor.jumpForward tui.editor.pageDown tui.editor.pageUp tui.editor.undo tui.editor.yank tui.editor.yankPop tui.input.copy tui.input.newLine tui.input.submit tui.input.tab tui.select.cancel tui.select.confirm tui.select.down tui.select.pageDown tui.select.pageUp tui.select.up`
	out := make(map[string]string)
	for property := range strings.FieldsSeq(properties) {
		out[property] = "named TUI action binding with one key or an ordered key list"
	}
	return out
}()

func reviewedSupportBehavior(root, property string) string {
	if root == "pkg:tui/.#Keybindings" {
		return reviewedKeybindingsProperties[property]
	}
	return reviewedSupportProperties[root][property]
}

var terminalBehavior = map[string]string{
	"start": "takes terminal ownership, installs input/resize handlers, and enters raw/keyboard protocol state", "stop": "restores terminal input/protocol state and removes handlers", "drainInput": "asynchronously drains pending input until idle or max duration", "write": "writes exact bytes to terminal output",
	"columns": "synchronously returns current terminal columns", "rows": "synchronously returns current terminal rows", "kittyProtocolActive": "synchronously returns negotiated Kitty keyboard state", "moveBy": "moves cursor relatively by signed line count", "hideCursor": "emits cursor-hide control", "showCursor": "emits cursor-show control",
	"clearLine": "clears the current terminal line", "clearFromCursor": "clears from cursor through screen end", "clearScreen": "clears screen and homes cursor", "setTitle": "sets terminal title to supplied text", "setProgress": "starts or clears terminal OSC progress indication",
}

var tuiBehavior = map[string]string{
	"mode": "readonly regular or fullscreen renderer identity", "children": "mutable ordered root component array", "terminal": "stable underlying Terminal reference", "onDebug": "optional synchronous debug-key callback", "fullRedraws": "readonly count of full repaint operations",
	"addChild": "appends one root component preserving insertion order", "removeChild": "removes the first strict-identical root component if mounted", "clear": "removes all root children", "getShowHardwareCursor": "returns hardware-cursor preference", "setShowHardwareCursor": "updates hardware-cursor preference and requests render", "getClearOnShrink": "returns regular-renderer shrink-clear preference", "setClearOnShrink": "updates shrink-clear preference",
	"setFocus": "synchronously transitions strict component focus and Focusable flags", "showOverlay": "synchronously appends one mounted overlay entry, conditionally focuses it, hides terminal cursor, requests render, and returns an identity handle", "hideOverlay": "permanently removes the append-stack tail independent of visual focus order", "hasOverlay": "returns whether any mounted entry is currently visible",
	"start": "starts terminal input/render lifecycle", "stop": "stops render timers and terminal lifecycle with optional preserve-screen behavior", "render": "renders root component lines for the supplied width", "renderNow": "cancels pending coalesced work and renders immediately; force resets renderer state", "requestRender": "coalesces demand to the 16 ms frame cadence; force marks the next frame full", "invalidate": "invalidates mounted roots and overlay components",
	"addInputListener": "adds a listener in Set insertion order and returns an identity disposer", "removeInputListener": "removes the strict-identical listener", "handleInput": "optional Component input method inherited by TUI shape", "handleMouse": "Container mouse dispatch inherited by TUI: routes an in-bounds event to the child under its row with child-local coordinates and returns the first claiming dispatch result", "wantsKeyRelease": "optional Component release opt-in inherited by TUI shape",
	"onTerminalColorSchemeChange": "registers a color-scheme listener and returns an identity disposer", "setTerminalColorSchemeNotifications": "idempotently enables or disables terminal color notifications", "queryTerminalBackgroundColor": "writes OSC 11 query and asynchronously returns parsed RGB or undefined at timeout", "queryTerminalColorScheme": "writes color-scheme query and asynchronously returns scheme or undefined at timeout",
}

var themeBehavior = map[string]string{
	"name": "readonly optional theme name", "sourcePath": "readonly optional source path", "sourceInfo": "optional source provenance", "fg": "wraps text with selected foreground ANSI and domain-preserving reset", "bg": "wraps text with selected background ANSI and domain-preserving reset",
	"bold": "wraps text in bold and restores prior attribute state", "italic": "wraps text in italic and restores prior attribute state", "underline": "wraps text in underline and restores prior attribute state", "strikethrough": "wraps text in strikethrough and restores prior attribute state", "inverse": "wraps text in inverse video and restores prior attribute state",
	"getFgAnsi": "returns raw ANSI prefix for a foreground domain", "getBgAnsi": "returns raw ANSI prefix for a background domain", "getColorMode": "returns truecolor or 256color mode", "getThinkingBorderColor": "returns the styling closure selected for a ThinkingLevel", "getBashModeBorderColor": "returns the bash-mode border styling closure",
}

var keybindingBehavior = map[string]string{
	"matches": "matches raw legacy or Kitty/CSI-u input against the current ordered keys for one action", "getKeys": "returns current ordered KeyId values for one action", "getDefinition": "returns the registered action definition", "getConflicts": "returns conflicts derived from effective bindings", "setUserBindings": "atomically replaces user overrides and recomputes resolved bindings",
	"getUserBindings": "returns current user override configuration", "getResolvedBindings": "returns the ordered resolved action mapping", "reload": "reloads config-path bindings in place so captured manager identity stays stable", "getEffectiveConfig": "returns the effective configured action-to-key mapping",
}

func memberProperty(id string) string {
	marker := "::property:"
	_, after, ok := strings.Cut(id, marker)
	if !ok {
		return ""
	}
	rest := after
	if end := strings.Index(rest, "::"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func isConstruct(id string) bool { return strings.Contains(id, "::construct:") }

func memberTiming(root, property string) string {
	if property == "drainInput" || property == "queryTerminalBackgroundColor" || property == "queryTerminalColorScheme" || (root == "pkg:coding-agent/.#ExtensionUIContext::property:custom" && property == "custom") {
		return "asynchronous-promise"
	}
	if property == "requestRender" {
		return "synchronous-coalesced-demand"
	}
	if property == "renderNow" {
		return "synchronous-immediate"
	}
	if property == "addInputListener" || property == "onTerminalColorSchemeChange" {
		return "synchronous-registration-with-synchronous-disposer"
	}
	if property == "visible" {
		return "synchronous-callback-evaluated-each-visibility-cycle"
	}
	return "synchronous"
}

func memberError(root, property string) string {
	if property == "drainInput" || property == "queryTerminalBackgroundColor" || property == "queryTerminalColorScheme" {
		return "Promise resolves undefined on supported timeout paths; transport/runtime rejection remains observable where declared"
	}
	if root == "pkg:coding-agent/.#ExtensionUIContext::property:custom" {
		return "factory throw or Promise rejection rejects while open; first completion wins and later outcomes cannot replace it"
	}
	if root == "pkg:coding-agent/.#Theme" {
		return "typed color domains exclude unsupported names; runtime-invalid styling input throws locally"
	}
	if property == "render" || property == "handleInput" || property == "visible" || strings.Contains(property, "Listener") || property == "onDebug" {
		return "callback exceptions propagate on the local event-loop invocation; no callback runs under host overlay/compositor locks"
	}
	return "no error return is declared; invalid or unavailable operations follow the cited synchronous implementation"
}

// absentInPig lists pinned members that Pig does not implement yet: the
// normalized mouse API, rendered overlay bounds, and the added alternate-screen
// navigation and search actions.
func absentInPig(root, property string) bool {
	switch {
	case strings.HasPrefix(root, "pkg:tui/.#TuiMouse"), root == "pkg:tui/.#OverlayBounds":
		return true
	case property == "handleMouse" || (root == "pkg:tui/.#OverlayHandle" && property == "getBounds"):
		return true
	case root == "pkg:tui/.#Keybindings":
		switch property {
		case "tui.altScreen.halfPageDown", "tui.altScreen.halfPageUp", "tui.altScreen.lineDown", "tui.altScreen.lineUp", "tui.altScreen.search", "tui.altScreen.searchClose", "tui.altScreen.searchNext", "tui.altScreen.searchPrevious":
			return true
		}
	}
	return false
}

func currentPigDisposition(root, property string) string {
	if absentInPig(root, property) {
		return "absent in Pig; the pinned member is not implemented"
	}
	switch root {
	case "pkg:ai/.#ThinkingLevel", "pkg:tui/.#TuiMode", "pkg:coding-agent/.#SourceInfo", "pkg:tui/.#KeyId", "pkg:tui/.#Keybinding", "pkg:tui/.#KeybindingConflict", "pkg:tui/.#KeybindingDefinition", "pkg:tui/.#Keybindings", "pkg:tui/.#KeybindingsConfig", "pkg:tui/.#RgbColor", "pkg:tui/.#TerminalColorScheme", "pkg:tui/.#TuiStopOptions":
		return "source-reviewed support type; public custom-factory realization deferred"
	case "pkg:tui/.#OverlayOptions", "pkg:tui/.#OverlayHandle", "pkg:tui/.#OverlayMargin", "pkg:tui/.#OverlayUnfocusOptions", "pkg:tui/.#SizeValue", "pkg:tui/.#OverlayAnchor":
		return "private-pr-a-foundation-only; exported extension contract remains reduced"
	case "pkg:tui/.#Component", "pkg:tui/.#Focusable":
		return "partial-host-contract; remote factory projection remains reduced"
	case "pkg:tui/.#Terminal":
		return "host-implemented; not projected to custom factories"
	case "pkg:tui/.#TUI":
		if property == "showOverlay" || property == "hideOverlay" || property == "hasOverlay" || property == "setFocus" {
			return "private-pr-a-foundation-only; custom factory projection absent"
		}
		return "host-implemented-or-partial; custom factory projection reduced"
	case "pkg:coding-agent/.#Theme", "pkg:coding-agent/.#ThemeColor":
		return "host-theme-present; stable live runtime proxy absent"
	case "pkg:coding-agent/.#KeybindingsManager":
		return "host-manager-present; configured factory projection absent"
	case "pkg:coding-agent/.#ExtensionUIContext::property:custom":
		return "reduced current custom contract; public shape intentionally unchanged in pr-a"
	case "pkg:tui/.#TuiInputListener", "pkg:tui/.#TuiInputListenerResult":
		return "host listener behavior present; remote factory projection absent"
	default:
		return "designed-out-or-unclassified"
	}
}

func pigTargets(root string) []string {
	switch {
	case root == "pkg:coding-agent/.#SourceInfo":
		return []string{"tui/theme.go"}
	case root == "pkg:tui/.#KeyId" || strings.HasPrefix(root, "pkg:tui/.#Keybinding"):
		return []string{"internal/codingagent/keybindings.go"}
	case root == "pkg:tui/.#RgbColor" || root == "pkg:tui/.#TerminalColorScheme" || root == "pkg:tui/.#TuiStopOptions":
		return []string{"tui/tui.go"}
	case root == "pkg:ai/.#ThinkingLevel":
		return []string{"ai/types.go", "tui/theme.go"}
	case root == "pkg:tui/.#TuiMode", strings.HasPrefix(root, "pkg:tui/.#TuiMouse"):
		return []string{"tui/tui.go"}
	case strings.HasPrefix(root, "pkg:tui/.#Overlay"), root == "pkg:tui/.#SizeValue", root == "pkg:tui/.#Component", root == "pkg:tui/.#Focusable":
		return []string{"tui/overlay_model.go", "tui/overlay_command.go", "tui/overlay_compositor.go"}
	case root == "pkg:tui/.#Terminal":
		return []string{"tui/terminal.go"}
	case root == "pkg:tui/.#TUI", root == "pkg:tui/.#TuiInputListener", root == "pkg:tui/.#TuiInputListenerResult":
		return []string{"tui/tui.go", "tui/tui_alt_screen.go"}
	case root == "pkg:coding-agent/.#Theme", root == "pkg:coding-agent/.#ThemeColor":
		return []string{"tui/theme.go", "coding/extension/host/subprocess/runtime-node/runtime.mjs"}
	case root == "pkg:coding-agent/.#KeybindingsManager":
		return []string{"internal/codingagent/keybindings.go", "coding/extension/host/subprocess/runtime-node/runtime.mjs"}
	default:
		return []string{"internal/codingagent/ext_ui_context.go", "coding/extension/host/subprocess/ui_bridge.go"}
	}
}

func memberIdentity(root, property string) string {
	switch {
	case root == "pkg:tui/.#TuiInputListener":
		return "strict callback reference identity; removal targets the same function object"
	case root == "pkg:tui/.#TUI" && (property == "addInputListener" || property == "removeInputListener"):
		return "strict listener reference identity plus one registration disposer identity"
	case root == "pkg:tui/.#TUI" && property == "showOverlay":
		return "strict component identity and one stable identity-targeted OverlayHandle"
	case root == "pkg:tui/.#OverlayHandle":
		return "one stable handle bound to one mounted-entry identity"
	case root == "pkg:tui/.#Component", root == "pkg:tui/.#Focusable":
		return "strict local component object identity"
	case root == "pkg:coding-agent/.#Theme":
		return "stable proxy identity; property access observes current backing Theme"
	case root == "pkg:coding-agent/.#KeybindingsManager":
		return "stable manager identity updated in place on reload"
	case root == "pkg:tui/.#TUI", root == "pkg:tui/.#Terminal":
		return "stable local host object identity"
	case root == "pkg:coding-agent/.#ExtensionUIContext::property:custom":
		return "stable local TUI, Theme, KeybindingsManager, done, component, and handle identities; executable identity never crosses transport"
	default:
		return "value-semantic declaration"
	}
}

func citationMember(property string) string {
	if property == "" {
		return ""
	}
	return "." + property
}

func memberCitation(root, property string) string {
	switch root {
	case "pkg:coding-agent/.#SourceInfo":
		return "packages/coding-agent/src/modes/interactive/theme/theme.ts#SourceInfo" + citationMember(property)
	case "pkg:tui/.#KeyId", "pkg:tui/.#Keybinding", "pkg:tui/.#KeybindingConflict", "pkg:tui/.#KeybindingDefinition", "pkg:tui/.#Keybindings", "pkg:tui/.#KeybindingsConfig":
		return "packages/tui/src/keybindings.ts" + citationMember(property)
	case "pkg:tui/.#RgbColor", "pkg:tui/.#TerminalColorScheme", "pkg:tui/.#TuiStopOptions":
		return "packages/tui/src/tui.ts" + citationMember(property)
	case "pkg:ai/.#ThinkingLevel":
		return "packages/ai/src/types.ts#ThinkingLevel"
	case "pkg:tui/.#TuiMode":
		return "packages/tui/src/tui.ts#TuiMode"
	case "pkg:tui/.#Terminal":
		return "packages/tui/src/terminal.ts:57-102#Terminal" + citationMember(property)
	case "pkg:tui/.#Component", "pkg:tui/.#Focusable":
		return "packages/tui/src/tui.ts:20-79#" + strings.TrimPrefix(root, "pkg:tui/.#") + citationMember(property)
	case "pkg:tui/.#TuiMouseEvent", "pkg:tui/.#TuiMouseEventResult", "pkg:tui/.#TuiMouseEventType", "pkg:tui/.#TuiMouseButton", "pkg:tui/.#OverlayBounds":
		return "packages/tui/src/tui.ts#" + strings.TrimPrefix(root, "pkg:tui/.#") + citationMember(property)
	case "pkg:tui/.#OverlayOptions", "pkg:tui/.#OverlayMargin", "pkg:tui/.#OverlayHandle", "pkg:tui/.#OverlayUnfocusOptions", "pkg:tui/.#SizeValue", "pkg:tui/.#OverlayAnchor":
		return "packages/tui/src/tui.ts:84-190#" + strings.TrimPrefix(root, "pkg:tui/.#") + citationMember(property)
	case "pkg:tui/.#TUI", "pkg:tui/.#TuiInputListener", "pkg:tui/.#TuiInputListenerResult":
		return "packages/tui/src/tui.ts:291-318,354-683,835-894,949-1171#TuiBase" + citationMember(property)
	case "pkg:coding-agent/.#Theme", "pkg:coding-agent/.#ThemeColor":
		return "packages/coding-agent/src/modes/interactive/theme/theme.ts:110-165,338-445,813-893#Theme" + citationMember(property)
	case "pkg:coding-agent/.#KeybindingsManager":
		if property == "reload" || property == "getEffectiveConfig" || property == "" {
			return "packages/coding-agent/src/core/keybindings.ts:340-390#KeybindingsManager" + citationMember(property)
		}
		return "packages/tui/src/keybindings.ts:191-262#KeybindingsManager" + citationMember(property) + "; packages/coding-agent/src/core/keybindings.ts:340-390#KeybindingsManager inheritance"
	default:
		return "packages/coding-agent/src/core/extensions/types.ts:195-210; packages/coding-agent/src/modes/interactive/interactive-mode.ts:2642-2716#showExtensionCustom"
	}
}

func realizationDispositions(root string, unsafe bool) []realizationDisposition {
	if unsafe {
		return []realizationDisposition{{Name: "host", Status: "host-owned-not-authorized-for-extension", Evidence: "packages/tui/src/tui.ts; packages/tui/src/terminal.ts"}}
	}
	partial := func(name, evidence string) realizationDisposition {
		return realizationDisposition{Name: name, Status: "partial-current-reduced-contract; atomic public realization deferred", Evidence: evidence}
	}
	absent := func(name, evidence string) realizationDisposition {
		return realizationDisposition{Name: name, Status: "absent-current; atomic public realization deferred", Evidence: evidence}
	}
	notApplicable := func(name, evidence string) realizationDisposition {
		return realizationDisposition{Name: name, Status: "not-applicable-current-production-architecture", Evidence: evidence}
	}
	return []realizationDisposition{
		partial("node-isolated", "coding/extension/host/subprocess/runtime-node/runtime.mjs"),
		partial("go-isolated", "extensions/sdk/context.go"),
		partial("go-packed", "coding/extension/host/subprocess/cell_plan.go"),
		partial("go-d31-fused", "coding/extension/host/subprocess/host.go"),
		absent("go-native-in-process", "internal/codingagent/ext_ui_context.go"),
		partial("rust-isolated", "extensions/sdk-rs/src/context.rs"),
		partial("rust-packed", "coding/extension/host/subprocess/cell_plan.go"),
		partial("python-isolated", "extensions/sdk-py/pig_sdk/__init__.py"),
		partial("python-packed", "coding/extension/host/subprocess/cell_plan.go"),
		notApplicable("node-packed", "coding/extension/host/subprocess/cell_plan.go packs Go, Rust, and Python only"),
		notApplicable("rust-fused", "no production launcher"), notApplicable("python-fused", "no production launcher"),
		notApplicable("node-native", "no production launcher"), notApplicable("rust-native", "no production launcher"), notApplicable("python-native", "no production launcher"),
	}
}

func memberDisposition(root, property string) string {
	if root == "pkg:ai/.#ThinkingLevel" || root == "pkg:tui/.#TuiMode" || root == "pkg:coding-agent/.#SourceInfo" || root == "pkg:tui/.#KeyId" || strings.HasPrefix(root, "pkg:tui/.#Keybinding") || root == "pkg:tui/.#RgbColor" || root == "pkg:tui/.#TerminalColorScheme" || root == "pkg:tui/.#TuiStopOptions" {
		return "source-reviewed support type required by the exact custom-factory closure"
	}
	if root == "pkg:tui/.#Terminal" || (root == "pkg:tui/.#TUI" && (property == "terminal" || property == "start" || property == "stop" || property == "clear" || property == "setFocus" || property == "setShowHardwareCursor" || property == "setClearOnShrink")) {
		return "explicit-safety-review-required; host ownership not authorized by draft"
	}
	if root == "pkg:tui/.#OverlayOptions" || root == "pkg:tui/.#OverlayHandle" || root == "pkg:tui/.#OverlayMargin" || root == "pkg:tui/.#OverlayUnfocusOptions" || root == "pkg:tui/.#SizeValue" || root == "pkg:tui/.#OverlayAnchor" || root == "pkg:tui/.#Component" || root == "pkg:tui/.#Focusable" {
		return "private-foundation-shaped; public realization deferred to atomic contract"
	}
	if root == "pkg:coding-agent/.#Theme" || root == "pkg:coding-agent/.#ThemeColor" {
		return "stable-live-proxy realization deferred to atomic contract"
	}
	if root == "pkg:coding-agent/.#KeybindingsManager" {
		return "in-place replicated-manager realization deferred to atomic contract"
	}
	if root == "pkg:tui/.#TuiInputListener" || root == "pkg:tui/.#TuiInputListenerResult" || (root == "pkg:tui/.#TUI" && (property == "addInputListener" || property == "removeInputListener")) {
		return "ordered local listener realization deferred to atomic contract"
	}
	return "source-compatible implementation or approved divergence required in atomic contract"
}
