package ai

// oaiMessageKey records every value of m that its encoding reads (messageKey). It reports false for a message it cannot record whole, which is then encoded each time.
//
// A field added to oaiMessage or what it holds must be written here: TestMessageKeysReadEveryField fails for a field the record does not read.
func oaiMessageKey(k *messageKey, m *oaiMessage) bool {
	k.text(m.Role)
	if !k.oaiValue(m.Content) {
		return false
	}
	k.text(m.ToolCallID)
	k.text(m.ToolName)
	k.length(len(m.ToolCalls), m.ToolCalls == nil)
	for i := range m.ToolCalls {
		call := &m.ToolCalls[i]
		k.text(call.ID)
		k.text(call.Type)
		k.present(call.Function != nil)
		if call.Function != nil {
			k.text(call.Function.Name)
			k.text(call.Function.Arguments)
		}
		k.present(call.Custom != nil)
		if call.Custom != nil {
			k.text(call.Custom.Name)
			k.text(call.Custom.Input)
		}
	}
	k.length(len(m.ReasoningDetails), m.ReasoningDetails == nil)
	for _, detail := range m.ReasoningDetails {
		k.present(detail != nil)
		k.raw(detail)
	}
	k.optionalText(m.ReasoningContent)
	k.text(m.Reasoning)
	k.text(m.ReasoningText)
	k.length(len(m.Tools), m.Tools == nil)
	for i := range m.Tools {
		if !k.oaiTool(&m.Tools[i]) {
			return false
		}
	}
	return k.ok()
}

// oaiValue writes the content of a message: text, or the parts of one.
func (k *messageKey) oaiValue(value any) bool {
	parts, isParts := value.([]oaiContentPart)
	if !isParts {
		return k.json(value)
	}
	k.tag(keyParts)
	k.length(len(parts), parts == nil)
	for i := range parts {
		part := &parts[i]
		k.text(part.Type)
		k.optionalText(part.Text)
		k.present(part.ImageURL != nil)
		if part.ImageURL != nil {
			k.text(part.ImageURL.URL)
		}
		k.oaiCacheControl(part.CacheControl)
	}
	return k.ok()
}

func (k *messageKey) oaiTool(t *oaiTool) bool {
	k.text(t.Type)
	k.present(t.Function != nil)
	if function := t.Function; function != nil {
		k.order(function.parameterOrder)
		k.text(function.Name)
		k.text(function.Description)
		if !k.object(function.Parameters) {
			return false
		}
		k.optionalBool(function.Strict)
	}
	k.present(t.Custom != nil)
	if custom := t.Custom; custom != nil {
		k.text(custom.Name)
		k.text(custom.Description)
		k.text(custom.Format.Type)
		k.text(custom.Format.Grammar.Syntax)
		k.text(custom.Format.Grammar.Definition)
	}
	k.oaiCacheControl(t.CacheControl)
	return k.ok()
}

func (k *messageKey) oaiCacheControl(c *oaiCacheControl) {
	k.present(c != nil)
	if c != nil {
		k.text(c.Type)
		k.text(c.TTL)
	}
}
