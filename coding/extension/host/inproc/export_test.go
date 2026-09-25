package inproc

import "github.com/MichaelKinsy/PiG/coding/extension"

// FireErrorForTest exposes the package-private emitError method to
// _test.go files in the inproc_test package. Test-only seam.
//
//nolint:revive // exported because cross-package _test files need access.
func FireErrorForTest(r *Runner, err *extension.ExtensionError) {
	r.emitError(err)
}

// AssertActiveForTest exposes the package-private assertActive method
// to _test.go files in the inproc_test package. Used by tests that
// construct an extension.Context directly (without going through
// dispatchContext) and need a real assertActive function.
//
//nolint:revive // exported because cross-package _test files need access.
func (r *Runner) AssertActiveForTest() error { return r.assertActive() }

// ExtractToolResultFieldsForTest exposes the package-private
// extractToolResultFields helper to _test.go files. Used by the
// exhaustiveness gate test that walks all 8 ToolResultEvent variants.
//
//nolint:revive // exported because cross-package _test files need access.
func ExtractToolResultFieldsForTest(event extension.ToolResultEvent) (content []any, details any, isError bool, usage any) {
	return extractToolResultFields(event)
}

// WithToolResultFieldsForTest exposes withToolResultFields.
//
//nolint:revive // exported because cross-package _test files need access.
func WithToolResultFieldsForTest(event extension.ToolResultEvent, content []any, details any, isError bool, usage any) extension.ToolResultEvent {
	return withToolResultFields(event, content, details, isError, usage)
}

// SessionBeforeIsCancelForTest exposes the package-private
// sessionBeforeIsCancel helper to _test.go files. Used by the
// exhaustiveness gate test that walks all 4 SessionBefore*Result
// variants.
//
//nolint:revive // exported because cross-package _test files need access.
func SessionBeforeIsCancelForTest(result any) bool {
	return sessionBeforeIsCancel(result)
}

// IsSessionBeforeEventForTest exposes the package-private
// isSessionBeforeEvent helper to _test.go files. Used by the
// exhaustiveness gate test that walks all 4 SessionBefore* event
// variants.
//
//nolint:revive // exported because cross-package _test files need access.
func IsSessionBeforeEventForTest(event any) bool {
	return isSessionBeforeEvent(event)
}
