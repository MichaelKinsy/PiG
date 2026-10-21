package storage_test

// pi: packages/durable/src/types.ts (TaskRecord.trace/links, SubmissionRecord.trace; Pi 2ec55d80)

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// Stored telemetry carriers and creation-time links persist as plain record members on every backend, with no migration.
func TestStorageKeepsTaskAndSubmissionTelemetryCarriers(t *testing.T) {
	for _, backend := range storageBenchmarkBackends {
		t.Run(backend, func(t *testing.T) {
			if err := durabletest.CheckRecordCarriers(createFixture(t, backend)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
