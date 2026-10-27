// Ports the part of packages/durable/test/harness-restart-failure.test.ts (Pi b3a29f11) that stays with the reference Harness:
// what a settings source resolves on the normal path. Every other case runs over both engines in
// durable/core/driver (harness_restart_failure_core_test.go).

package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// countingSettings is a settings source that counts its resolutions.
type countingSettings struct{ reads int }

func (settings *countingSettings) Settings() durable.Settings {
	settings.reads++
	return durable.Settings{RestartLimit: 5}
}

func TestTheRestartLimitOnTheNormalPath(t *testing.T) {
	// #11139: only a task that was cut off reads the limit, so the normal path resolves the settings no more often than
	// before the limit applied to every kind (harness-restart-failure.test.ts counts the resolutions of a whole round).
	t.Run("resolves the settings only for a task that was cut off", func(t *testing.T) {
		settings := &countingSettings{}
		if CutOffTooOften(0, settings) {
			t.Fatal("a task that was never cut off is cut off too often")
		}
		if settings.reads != 0 {
			t.Fatalf("settings resolved %d times for a task that was never cut off, want none", settings.reads)
		}
		if CutOffTooOften(4, settings) || settings.reads != 1 {
			t.Fatalf("4 restarts under a limit of 5: cut off too often, or %d reads, want 1", settings.reads)
		}
		if !CutOffTooOften(5, settings) {
			t.Fatal("5 restarts under a limit of 5 are not cut off too often")
		}
	})
}
