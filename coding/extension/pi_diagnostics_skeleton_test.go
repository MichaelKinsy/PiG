//go:build portmap_skeleton

package extension

import "testing"

// pi-unproven: packages/coding-agent/src/core/diagnostics.ts
//
// Skeleton for the marker test of packages/coding-agent/src/core/diagnostics.ts. Port its cases against coding/extension/diagnostics.go: call the entry point, assert the Pi result, then
// delete the build tag line, change `pi-unproven` to `pi`, and prove the test red by mutating a covered line of the cited file.
//
// Entry points in the cited file:
//   - type ResourceCollision
//   - type ResourceDiagnostic
func TestPiCodingAgentSrcCoreDiagnosticsSkeleton(t *testing.T) {
	t.Fatal("skeleton: port the diagnostics.ts cases against coding/extension/diagnostics.go")
}
