package extension

// ResourceCollision describes two resources that claim the same name: the winner is kept and the loser is skipped. ResourceType is
// "extension", "skill", "prompt" or "theme"; WinnerSource and LoserSource are package sources such as "npm:foo" or "local".
//
// upstream: core/diagnostics.ts:1-8 (export interface ResourceCollision)
type ResourceCollision struct {
	ResourceType string `json:"resourceType"`
	Name         string `json:"name"`
	WinnerPath   string `json:"winnerPath"`
	LoserPath    string `json:"loserPath"`
	WinnerSource string `json:"winnerSource,omitempty"`
	LoserSource  string `json:"loserSource,omitempty"`
}

// ResourceDiagnostic is a warning, error or collision reported while the runner and the resource loader resolve extensions,
// skills, prompts and themes.
//
// upstream: core/diagnostics.ts:10-15 (export interface ResourceDiagnostic)
type ResourceDiagnostic struct {
	// Type is one of "warning", "error", "collision". Mirrors the
	// upstream literal union. Not enforced by the Go type system -
	// callers are expected to use the constants below.
	Type string `json:"type"`

	// Message is the human-readable diagnostic text. Format matches
	// upstream verbatim where the diagnostic is user-visible (e.g.
	// shortcut/command collision strings) so no fidelity drift in
	// diagnostic UIs.
	Message string `json:"message"`

	// Path is the extension's resolvedPath for attribution. Optional;
	// upstream uses `path?: string`.
	Path string `json:"path,omitempty"`

	// Collision carries structured details when Type == "collision".
	// upstream: ResourceCollision in core/diagnostics.ts.
	Collision *ResourceCollision `json:"collision,omitempty"`
}

// Diagnostic type constants matching upstream's literal union.
const (
	DiagnosticWarning   = "warning"
	DiagnosticError     = "error"
	DiagnosticCollision = "collision"
)
