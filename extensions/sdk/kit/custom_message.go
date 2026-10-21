package kit

// CustomMessage is coding-agent's CustomMessageComponent(message, undefined,
// getMarkdownTheme(), outputPad): the default rendering of an extension's
// message, "[customType]" over the content's text in a customMessageBg box. A
// message's custom renderer is a closure upstream and has no kind here; the
// message-renderer surfaces draw it. It draws with the active theme, and a node
// with an ID keeps its host component across frames.
type CustomMessage struct {
	ID string
	// CustomType is message.customType; Text the message's text content (a
	// content array's text blocks joined with newlines).
	CustomType string
	Text       string
	// OutputPad is the horizontal padding of the box; upstream's outputPad
	// setting is 0 or 1.
	OutputPad int
	// Expanded is setExpanded's argument, which the default rendering does
	// not draw.
	Expanded bool
}

// NewCustomMessage returns a CustomMessage with upstream's outputPad 1.
func NewCustomMessage(customType, text string) *CustomMessage {
	return &CustomMessage{CustomType: customType, Text: text, OutputPad: 1}
}

// SetExpanded is upstream setExpanded.
func (c *CustomMessage) SetExpanded(expanded bool) { c.Expanded = expanded }

// SetOutputPad is upstream setOutputPad.
func (c *CustomMessage) SetOutputPad(outputPad int) { c.OutputPad = outputPad }

func (*CustomMessage) isNode() {}

// WireNode implements [Wired].
func (c *CustomMessage) WireNode() WireForm {
	return WireForm{Kind: "custom-message", ID: c.ID, Fields: messageFields{CustomType: c.CustomType, Text: c.Text, OutputPad: padPtr(c.OutputPad), Expanded: c.Expanded}}
}
