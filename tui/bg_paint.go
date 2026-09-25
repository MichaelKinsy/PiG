package tui

import "strings"

// ─── Component-level background painting ─────────────────────────────────────
//
// Components call paintBgWith() to apply themed background colors to their
// rendered lines. Colors come from ActiveTheme() which is set at startup
// via DetectTheme() (dark/light auto-detection matching upstream).
//
// Upstream reference: tui/src/utils.ts:applyBackgroundToLine,
// components/box.ts:applyBg, theme/dark.json, theme/light.json.

// Convenience accessors for component code that paints backgrounds.
// These read from the active theme so dark/light mode works automatically.
func UserMessageBgOpen() string { return ActiveTheme().UserMessageBg }
func ToolPendingBgOpen() string { return ActiveTheme().ToolPendingBg }
func ToolSuccessBgOpen() string { return ActiveTheme().ToolSuccessBg }
func ToolErrorBgOpen() string   { return ActiveTheme().ToolErrorBg }
func BgClose() string           { return ActiveTheme().BgClose }

// paintBgWith wraps `line` in the given bg-open SGR + bgClose, padded
// to `width` visible columns. Visible width respects ANSI escapes (via
// lineDisplayWidth). Used by UserMessageBlock and ToolExecutionComponent.
//
// Body renderers (read, write, edit) emit \x1b[0m (full SGR reset) inside
// their styled lines. A bare open+line+close approach would lose the bg
// tint at every reset point, leaving "uncolored pixels" for the remainder
// of the line. This function re-applies `open` after every \x1b[0m so the
// bg tint is continuous regardless of embedded resets.
//
// If `width <= visibleLen(line)` the function emits exactly the line
// with no trailing padding (still bg-wrapped): caller is responsible
// for clamping over-width content before paint.
func paintBgWith(open, line string, width int) string {
	// Replace tabs with spaces: terminals don't reliably paint bg
	// color through tab stops, causing visible gaps in tinted boxes.
	line = strings.ReplaceAll(line, "\t", "   ")
	visLen := lineDisplayWidth(line)
	pad := max(width-visLen, 0)
	bgClose := ActiveTheme().BgClose
	// Re-apply bg after any full SGR reset inside the line so body
	// renderers that use \x1b[0m for dim/color don't break the bg tint.
	// Also handle the shorter \x1b[m form (semantically identical, used
	// by grep --color and many other tools).
	if open != "" && strings.Contains(line, "\x1b[") {
		// Strip \x1b[K (erase-to-EOL): it would clear the bg paint from
		// the current position to the end of the terminal row using whatever
		// bg color is active at that point.
		line = strings.ReplaceAll(line, "\x1b[K", "")
		line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+open)
		line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+open)
	}
	// \x1b[K (erase to end of line) uses the current bg color to fill
	// the remainder of the terminal row, preventing "stripes" when
	// lineDisplayWidth undercounts or the terminal is wider than width.
	return open + line + strings.Repeat(" ", pad) + "\x1b[K" + bgClose
}
