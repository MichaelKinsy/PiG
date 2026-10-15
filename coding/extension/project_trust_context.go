package extension

import "context"

// Ports packages/coding-agent/src/core/extensions/types.ts ProjectTrustContext and ProjectTrustHandler.

// ProjectTrustUI is the Pick<ExtensionUIContext, "select" | "confirm" | "input" | "notify"> a project_trust handler may use.
// Every UIContext satisfies it.
type ProjectTrustUI interface {
	Select(ctx context.Context, title string, options []string, opts ExtensionUIDialogOptions) (string, error)
	Confirm(ctx context.Context, title, message string, opts ExtensionUIDialogOptions) (bool, error)
	Input(ctx context.Context, title, placeholder string, opts ExtensionUIDialogOptions) (string, error)
	Notify(message, kind string)
}

// ProjectTrustContext is the context a project_trust handler receives before any runtime exists (types.ts:693).
type ProjectTrustContext struct {
	// Cwd is the project folder being decided.
	Cwd string
	// Mode is the run mode: "tui" for interactive, otherwise "print", "json" or "rpc".
	Mode ExtensionMode
	// HasUI reports whether the UI can prompt.
	HasUI bool
	UI    ProjectTrustUI
}

// ProjectTrustHandler decides trust for a project folder (types.ts:698). The first handler that returns "yes" or "no" wins;
// "undecided" falls through to the next handler.
type ProjectTrustHandler func(ctx context.Context, evt ProjectTrustEvent, trust ProjectTrustContext) (ProjectTrustEventResult, error)
