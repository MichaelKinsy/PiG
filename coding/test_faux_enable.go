package coding

import (
	"errors"
	"os"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
)

// testFauxProviderFactory builds the test-faux provider once EnableTestFauxProvider ran.
var testFauxProviderFactory atomic.Pointer[func() ai.Provider]

// EnableTestFauxProvider lets PIG_TEST_FAUX=1 build the test-only test-faux provider, whose scenario is PIG_TEST_FAUX_SCENARIO. The pig command calls it where it starts; a program that does not leaves the provider and its scenario classifier out of its binary (TestHeadlessEmbedBinarySizeGate).
func EnableTestFauxProvider() {
	factory := func() ai.Provider {
		//perf:ok once per Services construction under PIG_TEST_FAUX=1, which only tests set
		return &ai.TestFauxProvider{Scenario: os.Getenv("PIG_TEST_FAUX_SCENARIO")}
	}
	testFauxProviderFactory.Store(&factory)
}

// TestFauxProviderEnabled reports whether this program serves the test-faux provider: EnableTestFauxProvider ran and PIG_TEST_FAUX=1. The model registry lists test-faux, the model build accepts it and the pig command selects it under this one condition.
func TestFauxProviderEnabled() bool {
	//perf:ok once per Services construction, model build or startup model selection, never per event
	return testFauxProviderFactory.Load() != nil && os.Getenv("PIG_TEST_FAUX") == "1"
}

var errTestFauxNotEnabled = errors.New("coding: BuildModel: test-faux is not enabled in this program: call coding.EnableTestFauxProvider before building it")

// newTestFauxProvider builds the test-faux provider, or reports that EnableTestFauxProvider did not run.
func newTestFauxProvider() (ai.Provider, error) {
	factory := testFauxProviderFactory.Load()
	if factory == nil {
		return nil, errTestFauxNotEnabled
	}
	return (*factory)(), nil
}
