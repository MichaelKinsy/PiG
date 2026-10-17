package ai

// anthMessageKey records every value of m that its encoding reads (messageKey). It reports false for a message it cannot record whole, which is then encoded each time.
//
// A field added to anthMessage, anthContentBlock, anthToolChange, anthTool or what they hold must be written here: TestMessageKeysReadEveryField fails for a field the record does not read.
func anthMessageKey(k *messageKey, m *anthMessage) bool {
	k.text(m.Role)
	if !k.anthValue(m.Content) {
		return false
	}
	k.present(m.OutputConfig != nil)
	if m.OutputConfig != nil {
		k.text(string(m.OutputConfig.Effort))
	}
	return k.ok()
}

// anthValue writes a value of an any field of a message: a decoded JSON value, or the blocks a message or a tool result holds.
func (k *messageKey) anthValue(value any) bool {
	blocks, isBlocks := value.([]anthContentBlock)
	if !isBlocks {
		return k.json(value)
	}
	k.tag(keyBlocks)
	k.length(len(blocks), blocks == nil)
	for i := range blocks {
		if k.over {
			break
		}
		if !k.anthBlock(&blocks[i]) {
			return false
		}
	}
	return true
}

func (k *messageKey) anthBlock(b *anthContentBlock) bool {
	k.text(b.Type)
	k.text(b.Text)
	k.text(b.ID)
	k.text(b.Name)
	if !k.anthValue(b.Input) {
		return false
	}
	k.text(b.ToolUseID)
	if !k.anthValue(b.Content) {
		return false
	}
	k.optionalBool(b.IsError)
	if !k.anthValue(b.Source) {
		return false
	}
	k.text(b.Thinking)
	k.text(b.Signature)
	k.text(b.Data)
	k.present(b.Tool != nil)
	if b.Tool != nil {
		k.text(b.Tool.Type)
		k.text(b.Tool.Name)
		k.present(b.Tool.Definition != nil)
		if b.Tool.Definition != nil && !k.anthTool(b.Tool.Definition) {
			return false
		}
	}
	k.anthCacheControl(b.CacheControl)
	return k.ok()
}

func (k *messageKey) anthTool(t *anthTool) bool {
	k.text(t.Name)
	k.text(t.Description)
	if !k.json(t.EagerInputStream) {
		return false
	}
	k.optionalBool(t.Strict)
	if !k.object(t.InputSchema) {
		return false
	}
	k.order(t.inputSchemaOrder)
	k.boolean(t.DeferLoading)
	k.anthCacheControl(t.CacheControl)
	return k.ok()
}

func (k *messageKey) anthCacheControl(c *anthCacheControl) {
	k.present(c != nil)
	if c != nil {
		k.text(c.Type)
		k.text(c.TTL)
	}
}
