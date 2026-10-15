package extension

import "maps"

// SetMessageRenderer registers the renderer for a custom message type, replacing an earlier one. Pi's registerMessageRenderer writes the extension's own Map (loader.ts), which the runner reads live (runner.ts getMessageRenderer), so a renderer registered after load takes effect. Once InitializeDeclarationRegistry ran the registry is synchronized; before that the construction field is used.
func (e *Extension) SetMessageRenderer(customType string, renderer MessageRenderer) {
	if state := e.declarationState; state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.messageRenderers == nil {
			state.messageRenderers = map[string]MessageRenderer{}
		}
		state.messageRenderers[customType] = renderer
		return
	}
	if e.MessageRenderers == nil {
		e.MessageRenderers = map[string]MessageRenderer{}
	}
	e.MessageRenderers[customType] = renderer
}

// MessageRendererFor returns the renderer registered for customType, including one registered after load.
func (e *Extension) MessageRendererFor(customType string) (MessageRenderer, bool) {
	if state := e.declarationState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		renderer, ok := state.messageRenderers[customType]
		return renderer, ok
	}
	renderer, ok := e.MessageRenderers[customType]
	return renderer, ok
}

// SetEntryRenderer registers the renderer for a custom entry type, replacing an earlier one.
func (e *Extension) SetEntryRenderer(customType string, renderer EntryRenderer) {
	if state := e.declarationState; state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.entryRenderers == nil {
			state.entryRenderers = map[string]EntryRenderer{}
		}
		state.entryRenderers[customType] = renderer
		return
	}
	if e.EntryRenderers == nil {
		e.EntryRenderers = map[string]EntryRenderer{}
	}
	e.EntryRenderers[customType] = renderer
}

// EntryRendererFor returns the renderer registered for customType, including one registered after load.
func (e *Extension) EntryRendererFor(customType string) (EntryRenderer, bool) {
	if state := e.declarationState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		renderer, ok := state.entryRenderers[customType]
		return renderer, ok
	}
	renderer, ok := e.EntryRenderers[customType]
	return renderer, ok
}

// RegisteredRenderers returns copies of the message and entry renderer maps.
func (e *Extension) RegisteredRenderers() (messages map[string]MessageRenderer, entries map[string]EntryRenderer) {
	if state := e.declarationState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		return maps.Clone(state.messageRenderers), maps.Clone(state.entryRenderers)
	}
	return maps.Clone(e.MessageRenderers), maps.Clone(e.EntryRenderers)
}
