package tui

// custom_message.go: renders custom extension messages.
//
// Ports upstream custom-message.ts (99 LOC).
// In pig's line renderer, the Box/Spacer/Markdown composition is
// simplified to colored text lines.

import (
	"encoding/json"
	"strings"
)

// CustomMessageComponent renders a custom message entry from extensions.
type CustomMessageComponent struct {
	invalidatable
	CustomType string
	Content    string // text content (may contain markdown)
	Expanded   bool
}

// NewCustomMessageComponent creates a custom message renderer.
func NewCustomMessageComponent(customType, content string) *CustomMessageComponent {
	return &CustomMessageComponent{
		CustomType: customType,
		Content:    content,
	}
}

// SetExpanded toggles between collapsed and expanded rendering.
func (c *CustomMessageComponent) SetExpanded(expanded bool) {
	c.Expanded = expanded
	c.Invalidate()
}

// SetOutputPad invalidates the default renderer, whose box keeps its fixed inset.
// Only registered custom renderers consume the configured horizontal padding.
func (c *CustomMessageComponent) SetOutputPad(_ int) {
	c.Invalidate()
}

// Render produces the custom message lines.
// Mirrors upstream CustomMessageComponent which extends Container with a Box(1,1,bgFn).
// The Box adds paddingX=1 (space indent) and paddingY=1 (blank rows) with bg tint.
func (c *CustomMessageComponent) Render(width int) []string {
	if width < 4 {
		width = 4
	}
	t := ActiveTheme()
	customMsgBgOpen := t.CustomMessageBg
	var lines []string

	// The component spacer is outside the box's background-painted top padding.
	lines = append(lines, "", paintBgWith(customMsgBgOpen, "", width))

	const padding = " "
	// Label: [customType] in bold.
	labelStyled := t.CustomMessageLabel + "\x1b[1m[" + c.CustomType + "]\x1b[22m\x1b[0m"
	lines = append(lines, paintBgWith(customMsgBgOpen, padding+labelStyled, width))

	// Structural blank (upstream: Spacer(1) inside Box between label and content).
	lines = append(lines, paintBgWith(customMsgBgOpen, "", width))

	if c.Content != "" {
		// Render content as plain text lines with paddingX=1 indent.
		contentColor := t.CustomMessageText
		for raw := range strings.SplitSeq(c.Content, "\n") {
			var line string
			if contentColor != "" {
				line = padding + contentColor + raw + "\x1b[0m"
			} else {
				line = padding + raw
			}
			lines = append(lines, paintBgWith(customMsgBgOpen, line, width))
		}
	}

	lines = append(lines, paintBgWith(customMsgBgOpen, "", width))
	return lines
}

// CustomMessage holds the data for a custom message entry.
// Mirrors the upstream CustomMessage<T> shape.
type CustomMessage struct {
	CustomType string
	Content    any // string or []ContentBlock
}

// CustomMessageText extracts text from a CustomMessage's Content field the
// way upstream CustomMessageComponent does: a string is shown as is, and an
// array's text blocks are joined with newlines. Content from an extension is
// JSON-decoded ([]any of map[string]any); typed block slices from Go callers
// are read through the same JSON shape.
func CustomMessageText(cm *CustomMessage) string {
	if s, ok := cm.Content.(string); ok {
		return s
	}
	raw, err := json.Marshal(cm.Content)
	if err != nil {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}
