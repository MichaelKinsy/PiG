package codingagent

import "context"

// ReloadFromExtension uses the worker-facing action installed in the subprocess bridge.
func (h *TestHarness) ReloadFromExtension(ctx context.Context) error {
	return h.m.reloadFromExtension(ctx)
}

// Reload runs the real slash reload owner. Call it on the owner loop.
func (h *TestHarness) Reload() error { return h.m.buildSlashContext(h.ctx).Reload() }

// SetHideThinkingForReload changes the persisted input that the production reload reads.
func (h *TestHarness) SetHideThinkingForReload(hidden bool) error {
	return h.m.opts.SettingsManager.SetHideThinkingBlock(hidden)
}

// ReloadUIState observes focus and display settings on the owner loop.
func (h *TestHarness) ReloadUIState() (editorFocused, hideThinking bool) {
	return h.m.tuiInst.GetFocusedComponent() == h.m.editor, h.m.hideThinking
}

// SetTurnActive marks a run active or settled on the owner loop, as the run goroutine does.
func (h *TestHarness) SetTurnActive(active bool) { h.Do(func() { h.m.turnActive.Store(active) }) }
