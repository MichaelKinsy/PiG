package tui

// custom_message.go: renders custom extension messages.
//
// Ports upstream packages/coding-agent/src/modes/interactive/components/custom-message.ts.

import (
	"encoding/json"
	"strings"
)

// MessageRenderOptions is upstream MessageRenderOptions: the state a custom message renderer lays out for.
type MessageRenderOptions struct {
	Expanded  bool
	OutputPad int
}

// MessageRenderer is upstream MessageRenderer: it returns a styled component for a custom message, or nil to use the default
// box. A panic is treated as nil.
type MessageRenderer func(message *CustomMessage, options MessageRenderOptions) Component

// CustomMessageComponent renders a custom message entry from extensions with distinct styling.
// Mirrors upstream CustomMessageComponent extends Container (custom-message.ts:13).
type CustomMessageComponent struct {
	Container
	message         *CustomMessage
	customRenderer  MessageRenderer
	box             *Box
	customComponent Component
	markdownTheme   *MarkdownTheme
	expanded        bool
	outputPad       int
}

// NewCustomMessageComponent mirrors the upstream constructor (custom-message.ts:23): customRenderer may be nil, a nil
// markdownTheme selects the active theme's, and outputPad is the horizontal padding handed to customRenderer (upstream default 1).
func NewCustomMessageComponent(message *CustomMessage, customRenderer MessageRenderer, markdownTheme *MarkdownTheme, outputPad int) *CustomMessageComponent {
	c := &CustomMessageComponent{message: message, customRenderer: customRenderer, markdownTheme: markdownTheme, outputPad: outputPad}
	c.Add(NewSpacer(1))
	// Box with the custom message background (used for default rendering).
	c.box = NewPaddedBox(1, 1, func(text string) string { return paintBgWith(ActiveTheme().CustomMessageBg, text, 0) })
	c.rebuild()
	return c
}

// IsDirty reports a renderer component whose output changed on its own (an extension renderer proxy whose lines arrive after the first frame), so the parent container that caches this component re-renders it.
func (c *CustomMessageComponent) IsDirty() bool {
	if c.Container.IsDirty() {
		return true
	}
	dirty, ok := c.customComponent.(interface{ IsDirty() bool })
	return ok && dirty.IsDirty()
}

// pig additive (D91): SurfaceLive reports a renderer component that changes on its own, which
// IsDirty reads, so a TuiSurface rebuilds the message every frame.
func (c *CustomMessageComponent) SurfaceLive() bool {
	_, ok := c.customComponent.(interface{ IsDirty() bool })
	return ok
}

// SetExpanded rebuilds the content when the expansion state changes (custom-message.ts:41).
func (c *CustomMessageComponent) SetExpanded(expanded bool) {
	if c.expanded != expanded {
		c.expanded = expanded
		c.rebuild()
	}
}

// SetOutputPad rebuilds the content when the padding changes (custom-message.ts:48).
func (c *CustomMessageComponent) SetOutputPad(outputPad int) {
	if c.outputPad != outputPad {
		c.outputPad = outputPad
		c.rebuild()
	}
}

// Invalidate rebuilds the content, so a theme change reaches the box colors (custom-message.ts:55).
func (c *CustomMessageComponent) Invalidate() {
	c.Container.Invalidate()
	c.rebuild()
}

func (c *CustomMessageComponent) rebuild() {
	// Remove the previous content component.
	if c.customComponent != nil {
		c.Remove(c.customComponent)
		c.customComponent = nil
	}
	c.Remove(c.box)

	// The custom renderer goes first; it handles its own styling.
	if c.customRenderer != nil {
		if component := c.renderCustom(); component != nil {
			c.customComponent = component
			c.Add(component)
			return
		}
	}

	// Default rendering uses the box.
	c.Add(c.box)
	c.box.Clear()
	c.box.SetPaddingX(c.outputPad) // custom-message.ts:90

	// Label, then the content.
	label := ActiveTheme().Fg("customMessageLabel", "\x1b[1m["+c.message.CustomType+"]\x1b[22m")
	c.box.AddChild(NewText(label))
	c.box.AddChild(NewSpacer(1))

	markdown := NewMarkdownWithOptions(CustomMessageText(c.message), 0, 0, c.markdownTheme, &DefaultTextStyle{
		Color: func(text string) string { return ActiveTheme().Fg("customMessageText", text) },
	}, nil)
	c.box.AddChild(markdown)
}

// renderCustom returns the custom renderer's component, or nil when it returns none or panics (custom-message.ts:68-83 falls
// through to the default rendering on a throw).
func (c *CustomMessageComponent) renderCustom() (component Component) {
	defer func() {
		// upstream: packages/coding-agent/src/modes/interactive/components/custom-message.ts:customRenderer
		if recover() != nil {
			component = nil
		}
	}()
	return c.customRenderer(c.message, MessageRenderOptions{Expanded: c.expanded, OutputPad: c.outputPad})
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
