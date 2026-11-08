package theme

// Copied unchanged from coding-agent src/modes/interactive/theme/theme-tokens.ts (p2-tui-port.md section 5.3).
// Generated from apps/p2/src/tui/theme/theme-tokens.ts at Pi c23b9ec1; THEME_TOKENS order is the order of theme files and the published JSON Schema.

// Slot is the color slot of a theme token.
type Slot string

const (
	SlotForeground Slot = "foreground"
	SlotBackground Slot = "background"
)

// ThemeTokenDescriptor is theme-tokens.ts ThemeTokenDescriptor: Fallback names the token an omitted value falls back to (empty: required).
type ThemeTokenDescriptor struct {
	Name        string
	Slot        Slot
	Description string
	Fallback    string
}

// ThemeTokens is theme-tokens.ts THEME_TOKENS in declaration order.
var ThemeTokens = []ThemeTokenDescriptor{
	{Name: "accent", Slot: SlotForeground, Description: "Primary accent color (logo, selected items, cursor)"},
	{Name: "border", Slot: SlotForeground, Description: "Normal borders"},
	{Name: "borderAccent", Slot: SlotForeground, Description: "Highlighted borders"},
	{Name: "borderMuted", Slot: SlotForeground, Description: "Subtle borders"},
	{Name: "success", Slot: SlotForeground, Description: "Success states"},
	{Name: "error", Slot: SlotForeground, Description: "Error states"},
	{Name: "warning", Slot: SlotForeground, Description: "Warning states"},
	{Name: "muted", Slot: SlotForeground, Description: "Secondary/dimmed text"},
	{Name: "dim", Slot: SlotForeground, Description: "Very dimmed text (more subtle than muted)"},
	{Name: "text", Slot: SlotForeground, Description: "Default text color (usually empty string)"},
	{Name: "thinkingText", Slot: SlotForeground, Description: "Thinking block text color"},
	{Name: "scrollbarTrack", Slot: SlotForeground, Description: "Fullscreen scrollbar track foreground (falls back to muted when omitted)", Fallback: "muted"},
	{Name: "scrollbarThumb", Slot: SlotForeground, Description: "Fullscreen scrollbar thumb foreground (falls back to text when omitted)", Fallback: "text"},
	{Name: "selectedBg", Slot: SlotBackground, Description: "Selected item background"},
	{Name: "searchMatchBg", Slot: SlotBackground, Description: "Transcript search match background and current-match text (falls back to selectedBg when omitted)", Fallback: "selectedBg"},
	{Name: "searchMatchText", Slot: SlotForeground, Description: "Transcript search match text and current-match background (falls back to text when omitted)", Fallback: "text"},
	{Name: "userMessageBg", Slot: SlotBackground, Description: "User message background"},
	{Name: "userMessageText", Slot: SlotForeground, Description: "User message text color"},
	{Name: "customMessageBg", Slot: SlotBackground, Description: "Custom message background (hook-injected messages)"},
	{Name: "customMessageText", Slot: SlotForeground, Description: "Custom message text color"},
	{Name: "customMessageLabel", Slot: SlotForeground, Description: "Custom message type label color"},
	{Name: "toolPendingBg", Slot: SlotBackground, Description: "Tool execution box (pending state)"},
	{Name: "toolSuccessBg", Slot: SlotBackground, Description: "Tool execution box (success state)"},
	{Name: "toolErrorBg", Slot: SlotBackground, Description: "Tool execution box (error state)"},
	{Name: "toolTitle", Slot: SlotForeground, Description: "Tool execution box title color"},
	{Name: "toolOutput", Slot: SlotForeground, Description: "Tool execution box output text color"},
	{Name: "mdHeading", Slot: SlotForeground, Description: "Markdown heading text"},
	{Name: "mdLink", Slot: SlotForeground, Description: "Markdown link text"},
	{Name: "mdLinkUrl", Slot: SlotForeground, Description: "Markdown link URL"},
	{Name: "mdCode", Slot: SlotForeground, Description: "Markdown inline code"},
	{Name: "mdCodeBlock", Slot: SlotForeground, Description: "Markdown code block content"},
	{Name: "mdCodeBlockBorder", Slot: SlotForeground, Description: "Markdown code block fences"},
	{Name: "mdQuote", Slot: SlotForeground, Description: "Markdown blockquote text"},
	{Name: "mdQuoteBorder", Slot: SlotForeground, Description: "Markdown blockquote border"},
	{Name: "mdHr", Slot: SlotForeground, Description: "Markdown horizontal rule"},
	{Name: "mdListBullet", Slot: SlotForeground, Description: "Markdown list bullets/numbers"},
	{Name: "toolDiffAdded", Slot: SlotForeground, Description: "Added lines in tool diffs"},
	{Name: "toolDiffRemoved", Slot: SlotForeground, Description: "Removed lines in tool diffs"},
	{Name: "toolDiffContext", Slot: SlotForeground, Description: "Context lines in tool diffs"},
	{Name: "syntaxComment", Slot: SlotForeground, Description: "Syntax highlighting: comments"},
	{Name: "syntaxKeyword", Slot: SlotForeground, Description: "Syntax highlighting: keywords"},
	{Name: "syntaxFunction", Slot: SlotForeground, Description: "Syntax highlighting: function names"},
	{Name: "syntaxVariable", Slot: SlotForeground, Description: "Syntax highlighting: variable names"},
	{Name: "syntaxString", Slot: SlotForeground, Description: "Syntax highlighting: string literals"},
	{Name: "syntaxNumber", Slot: SlotForeground, Description: "Syntax highlighting: number literals"},
	{Name: "syntaxType", Slot: SlotForeground, Description: "Syntax highlighting: type names"},
	{Name: "syntaxOperator", Slot: SlotForeground, Description: "Syntax highlighting: operators"},
	{Name: "syntaxPunctuation", Slot: SlotForeground, Description: "Syntax highlighting: punctuation"},
	{Name: "thinkingOff", Slot: SlotForeground, Description: "Thinking level border: off"},
	{Name: "thinkingMinimal", Slot: SlotForeground, Description: "Thinking level border: minimal"},
	{Name: "thinkingLow", Slot: SlotForeground, Description: "Thinking level border: low"},
	{Name: "thinkingMedium", Slot: SlotForeground, Description: "Thinking level border: medium"},
	{Name: "thinkingHigh", Slot: SlotForeground, Description: "Thinking level border: high"},
	{Name: "thinkingXhigh", Slot: SlotForeground, Description: "Thinking level border: xhigh"},
	{Name: "thinkingMax", Slot: SlotForeground, Description: "Thinking level border: max (falls back to thinkingXhigh when omitted)", Fallback: "thinkingXhigh"},
	{Name: "bashMode", Slot: SlotForeground, Description: "Editor border color in bash mode"},
}

// ThemeToken, ThemeColor and ThemeBg are theme-tokens.ts ThemeToken, ThemeColor (a foreground token) and ThemeBg (a background token): token
// names are the strings of THEME_TOKENS.
type (
	ThemeToken = string
	ThemeColor = string
	ThemeBg    = string
)

var themeTokenIndex = func() map[string]ThemeTokenDescriptor {
	index := make(map[string]ThemeTokenDescriptor, len(ThemeTokens))
	for _, descriptor := range ThemeTokens {
		index[descriptor.Name] = descriptor
	}
	// theme-tokens.ts defineThemeTokens: a fallback names a known token of the same slot.
	for _, descriptor := range ThemeTokens {
		if descriptor.Fallback == "" {
			continue
		}
		fallback, ok := index[descriptor.Fallback]
		if !ok {
			panic("Theme token " + descriptor.Name + " has unknown fallback " + descriptor.Fallback)
		}
		if fallback.Slot != descriptor.Slot {
			panic("Theme token " + descriptor.Name + " has fallback " + descriptor.Fallback + " in a different color slot")
		}
	}
	return index
}()

// TokenDescriptor returns the descriptor of a theme token.
func TokenDescriptor(name string) (ThemeTokenDescriptor, bool) {
	descriptor, ok := themeTokenIndex[name]
	return descriptor, ok
}
