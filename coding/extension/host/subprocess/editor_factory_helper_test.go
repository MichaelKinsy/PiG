package subprocess

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// remoteEditorOfFactory is the editor in an extension's process that factory builds (extension.RemoteEditorFactory); nil for a nil factory.
func remoteEditorOfFactory(factory extension.EditorFactory) extension.RemoteEditor {
	if factory == nil {
		return nil
	}
	editor, _ := extension.RemoteEditorOf(factory(nil, tui.EditorTheme{}, nil))
	return editor
}
