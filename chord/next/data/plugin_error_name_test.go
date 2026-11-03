package data_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Pi packages/chord/src/next/data/errors.ts:96 `this.name = "PluginError"`: the error's own name, which err.name and
// String(err) read. D103: the inherited `name` is the error type's Name() method (as ai.ModelsError and delta.PathError).
func TestPluginErrorNameIsPluginError(t *testing.T) {
	var err error = data.NewPluginError("quota", "too many")
	named, ok := err.(interface{ Name() string })
	if !ok {
		t.Fatal("PluginError has no Name() string member")
	}
	if got := named.Name(); got != "PluginError" {
		t.Errorf("Name() = %q, want %q", got, "PluginError")
	}
}
