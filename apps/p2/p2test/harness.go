package p2test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// OpenHarness opens a Durable Harness over memory storage, for the tests of worker facets that read or commit to the
// session's Durable state (no model is called). It closes with the test.
func OpenHarness(t testing.TB) harness.Harness {
	t.Helper()
	opened, err := harness.OpenHarness(Background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models:   ai.CreateModels(ai.CreateModelsOptions{}),
		Registry: harness.CreateRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(context.WithoutCancel(Background)) })
	return opened
}

// HarnessParts are the worker natives for opened: its Harness only (a facet that needs the worker's environments reads
// none).
func HarnessParts(opened harness.Harness) *p2runtime.HarnessParts {
	return &p2runtime.HarnessParts{Durable: func() (harness.Harness, error) { return opened, nil }}
}
