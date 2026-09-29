package subprocess

import "context"

type terminalInputStateKey struct{}

type terminalInputState struct {
	editorText    string
	toolsExpanded bool
}

// WithTerminalInputState captures the UI values on the owner loop before a remote input listener runs. The request worker serializes this snapshot without reading mutable editor state or traversing Session history.
func WithTerminalInputState(ctx context.Context, editorText string, toolsExpanded bool) context.Context {
	return context.WithValue(ctx, terminalInputStateKey{}, terminalInputState{editorText: editorText, toolsExpanded: toolsExpanded})
}
