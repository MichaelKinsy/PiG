package extension

// EditorSyncError reports that an extension's installed editor refused the autocomplete provider the host handed it. Pi's
// setupAutocompleteProvider ends with `this.editor.setAutocompleteProvider?.(provider)`, so a throw there reaches whichever
// caller rebuilt the provider; the host reports it the way that caller does, and the extension named here is the one whose editor threw.
type EditorSyncError struct {
	// ExtensionPath is the path the extension was selected from, as [ExtensionError.ExtensionPath].
	ExtensionPath string
	// Err is the editor's own error, carrying its stack when the editor runs in a subprocess.
	Err error
}

func (e *EditorSyncError) Error() string { return e.Err.Error() }

func (e *EditorSyncError) Unwrap() error { return e.Err }
