package extension

// ResourceDiagnostic is the resolution-time warning/error/collision
// payload surfaced by the runner when extension resources (commands,
// shortcuts, flags, future: skills/prompts/themes) collide or fail to
// resolve cleanly.
//
// upstream: core/diagnostics.ts:10-16 (export interface ResourceDiagnostic)
//
// Final-home note. Upstream lives in `core/diagnostics.ts` because the
// type is shared between the extension runner and the resource loader
// (skills/prompts/themes). Until pig ports `core/resource-loader.ts`
// the type lives here in `coding/extension` to keep the dependency
// graph minimal. When that port lands, the canonical declaration moves
// to `coding/diagnostics/` and this declaration becomes a type alias
// for backward compatibility.
type ResourceCollision struct {
	ResourceType string `json:"resourceType"`
	Name         string `json:"name"`
	WinnerPath   string `json:"winnerPath"`
	LoserPath    string `json:"loserPath"`
	WinnerSource string `json:"winnerSource,omitempty"`
	LoserSource  string `json:"loserSource,omitempty"`
}

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
