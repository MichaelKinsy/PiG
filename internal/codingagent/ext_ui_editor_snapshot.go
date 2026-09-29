package codingagent

// pig additive (D19): synchronous SDK getters read immutable editor text published by the owner, not the editor's mutable lines from an IPC worker.
func (m *InteractiveMode) bindEditorSnapshot() {
	if m.editor == nil || m.editorSnapshotOwner == m.editor {
		return
	}
	editor := m.editor
	m.editorSnapshotOwner = editor
	text := editor.GetExpandedText()
	m.editorSnapshot.Store(&text)
	previous := editor.OnChange
	editor.OnChange = func(text string) {
		if m.editor == editor {
			expanded := editor.GetExpandedText()
			m.editorSnapshot.Store(&expanded)
		}
		if previous != nil {
			previous(text)
		}
	}
}
