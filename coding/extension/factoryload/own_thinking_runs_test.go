package factoryload_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// pig additive (D214): an in-process factory declares ownership while loading and changes it later through the API it kept.
func TestFactoryOwnsThinkingRunsAtLoadAndLater(t *testing.T) {
	var api extension.API
	runtime := extension.CreateExtensionRuntime()
	loaded, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		api = pi
		pi.OwnThinkingRuns(true)
		return nil
	}, t.TempDir(), extension.CreateEventBus(), runtime, "owner.go")
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{loaded}, t.TempDir(), runtime)
	if !runner.OwnsThinkingRuns() {
		t.Fatal("ownership declared while loading did not reach the runner")
	}
	api.OwnThinkingRuns(false)
	if runner.OwnsThinkingRuns() {
		t.Fatal("ownership withdrawn after loading did not reach the runner")
	}
	api.OwnThinkingRuns(true)
	if !runner.OwnsThinkingRuns() {
		t.Fatal("ownership declared after loading did not reach the runner")
	}
	other, err := factoryload.LoadExtensionFromFactory(func(extension.API) error { return nil }, t.TempDir(), extension.CreateEventBus(), runtime, "other.go")
	if err != nil {
		t.Fatal(err)
	}
	if other.OwnsThinkingRuns() {
		t.Fatal("an extension that never called OwnThinkingRuns owns thinking runs")
	}
}
