package harness

import "testing"

// packages/durable/src/harness/types.ts:467-476 (TaskInspection.state kinds and the blocked reasons) and :487
// (HarnessInspection.scheduling): the literal values are Pi's public values.
func TestTaskInspectionLiteralsMatchPi(t *testing.T) {
	kinds := map[TaskInspectionKind]string{
		TaskInspectionRunning:    "running",
		TaskInspectionReady:      "ready",
		TaskInspectionWaiting:    "waiting",
		TaskInspectionCompleting: "completing",
		TaskInspectionBlocked:    "blocked",
	}
	for kind, want := range kinds {
		if string(kind) != want {
			t.Errorf("kind %q, want %q", kind, want)
		}
	}
	reasons := map[string]string{blockedMissingTask: "missing_task", blockedTaskTooOld: "task_too_old", blockedMigrationFailed: "migration_failed"}
	for reason, want := range reasons {
		if reason != want {
			t.Errorf("reason %q, want %q", reason, want)
		}
	}
}
