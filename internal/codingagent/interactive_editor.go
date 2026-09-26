package codingagent

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) openExternalEditor(ctx context.Context) {
	initial := m.editor.Text()

	// Hand off the terminal: cursor visible, cooked mode.
	m.tuiInst.ShowCursor()
	if m.rawRestore != nil {
		m.rawRestore()
		m.rawRestore = nil
	}

	configuredEditor := ""
	if m.opts.SettingsManager != nil {
		configuredEditor = m.opts.SettingsManager.GetExternalEditorCommand()
	}
	result, runErr := OpenExternalEditor(ctx, initial, configuredEditor)

	// Re-enter raw mode for pig.
	restore, rawErr := tui.EnterRawMode()
	if rawErr == nil {
		m.rawRestore = restore
	}
	m.tuiInst.HideCursor()

	// Upstream handleOpenExternalEditor applies a completed edit and shows
	// nothing either way; a failed edit keeps the original text.
	if runErr == nil {
		m.editor.SetText(result)
	}

	// Force a full re-paint: external editor may have used the alt
	// screen, scrolled the viewport, or left cursor escapes behind.
	m.tuiInst.RepaintAll()
}

// setGenericToolArgs retains arguments only for extension tools that use the
// generic call renderer. Built-ins and tools with a custom call renderer keep
// their existing presentation contract.
func (m *InteractiveMode) setGenericToolArgs(comp *tui.ToolExecutionComponent, name string, args json.RawMessage) {
	if comp == nil || m.newRunner == nil {
		return
	}
	definition, ok := m.newRunner.GetToolDefinition(name)
	// An override of a built-in name without its own renderCall keeps the
	// built-in renderers (upstream renderers/index.ts withBuiltInRenderers).
	if !ok || definition.RenderCall != nil || tui.HasBuiltInToolRenderers(name) {
		return
	}
	// pig divergence (D59): generic extension cards retain complete args so
	// every collapsed omission is recoverable through the global details toggle.
	comp.SetStructuredArgs(args)
}

// toggleAllTools flips the global tool expansion state for the whole
// transcript. Mirrors upstream toggleToolOutputExpansion().
func (m *InteractiveMode) toggleAllTools() {
	m.toolMu.Lock()
	expanded := !m.toolsExpanded
	m.toolMu.Unlock()
	m.setAllToolsExpanded(expanded)
}

// setAllToolsExpanded applies a changed expansion state to the startup header and every visible expandable transcript component. Extension UI calls share this path so modal components can invoke the same app-level behavior as the default editor.
func (m *InteractiveMode) setAllToolsExpanded(expanded bool) {
	m.toolMu.Lock()
	if m.toolsExpanded == expanded {
		m.toolMu.Unlock()
		return
	}
	m.toolsExpanded = expanded
	m.builtInHeaderExpanded = expanded
	// Snapshot so we don't hold the lock while components invalidate.
	comps := make([]*tui.ToolExecutionComponent, len(m.toolOrder))
	copy(comps, m.toolOrder)
	bashes := make([]*tui.BashExecutionBlock, len(m.bashOrder))
	copy(bashes, m.bashOrder)
	compactions := make([]*tui.CompactionSummaryComponent, len(m.compactionOrder))
	copy(compactions, m.compactionOrder)
	branches := make([]*tui.BranchSummaryComponent, len(m.branchSummaryOrder))
	copy(branches, m.branchSummaryOrder)
	customMessages := make([]expandableCustomMessageComponent, len(m.customMessageOrder))
	copy(customMessages, m.customMessageOrder)
	m.toolMu.Unlock()
	for _, c := range comps {
		c.SetExpanded(expanded)
	}
	for _, b := range bashes {
		b.SetExpanded(expanded)
	}
	// Ctrl+O also toggles compaction summary chips.
	for _, cs := range compactions {
		cs.SetExpanded(expanded)
	}
	// Ctrl+O also toggles branch summary components.
	for _, bs := range branches {
		bs.SetExpanded(expanded)
	}
	for _, custom := range customMessages {
		custom.SetExpanded(expanded)
	}
	for _, section := range m.loadedResourceSections {
		section.SetExpanded(expanded)
	}
	m.showStatus("Tool output: " + map[bool]string{true: "expanded", false: "collapsed"}[expanded])
	if !expanded && m.tuiInst != nil {
		// Collapsing can remove more rows than the viewport contains. Those
		// expanded rows already live in native scrollback and differential
		// repainting cannot erase them, so rebuild the transcript in its
		// collapsed form. This clear is tied to the user's explicit action;
		// automatic tool completion follows the ordinary Pi redraw path.
		m.tuiInst.ForceFullRender()
	}
}

// rebuildChatFromSession rebuilds the visible conversation from the active
// path-to-leaf messages.
