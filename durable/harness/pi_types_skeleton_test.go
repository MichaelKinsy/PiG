//go:build portmap_skeleton

package harness

import "testing"

// pi-unproven: packages/durable/src/harness/types.ts
//
// Skeleton for the marker test of packages/durable/src/harness/types.ts. Port its cases against durable/harness/types.go: call the entry point, assert the Pi result, then
// delete the build tag line, change `pi-unproven` to `pi`, and prove the test red by mutating a covered line of the cited file.
//
// Pi tests of this file (port these cases):
//   - packages/durable/test/types.test.ts
//
// Entry points in the cited file:
//   - type BeforeToolResult
//   - type CompactionDecision
//   - type CompactionHooks
//   - type CompactionRequest
//   - type CompactionResult
//   - type Conversation
//   - type ConversationCreateOptions
//   - type ConversationInit
//   - type ConversationWatch
//   - type EnvTarget
//   - type GenerationHooks
//   - type GenerationRequest
//   - type Harness
//   - type HarnessInspection
//   - type HarnessOptions
//   - type HookApi
//   - type RootOptions
//   - type TaskInspection
//   - type TaskInspectionKind
//   - type TaskInspectionState
//   - type ToolHooks
//   - type YieldContinue
func TestPiDurableSrcHarnessTypesSkeleton(t *testing.T) {
	t.Fatal("skeleton: port the types.ts cases against durable/harness/types.go")
}
