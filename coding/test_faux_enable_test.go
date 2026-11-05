package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The registry lists test-faux, and BuildModel builds it, only when this program enabled the provider: a program that sets PIG_TEST_FAUX=1 without calling
// EnableTestFauxProvider must not see a model it cannot build.
func TestTestFauxIsListedOnlyWhenItCanBeBuilt(t *testing.T) {
	t.Setenv("PIG_TEST_FAUX", "1")
	previous := testFauxProviderFactory.Load()
	t.Cleanup(func() { testFauxProviderFactory.Store(previous) })
	for _, enabled := range []bool{false, true} {
		testFauxProviderFactory.Store(nil)
		if enabled {
			EnableTestFauxProvider()
		}
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		listed := slices.ContainsFunc(services.Registry().GetAll(), func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == "test-faux" })
		_, buildErr := BuildModel("test-faux/faux-1", services)
		services.Close()
		if listed != enabled {
			t.Errorf("enabled=%v: test-faux listed = %v", enabled, listed)
		}
		if built := buildErr == nil; built != enabled {
			t.Errorf("enabled=%v: BuildModel(test-faux/faux-1) = %v", enabled, buildErr)
		}
		if listed != (buildErr == nil) {
			t.Errorf("enabled=%v: the registry lists test-faux = %v but BuildModel succeeds = %v", enabled, listed, buildErr == nil)
		}
	}
}
