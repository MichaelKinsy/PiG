package subprocess

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// resource-loader.ts loadExtensionPaths takes the runtime of the load: UseRuntime makes the Host register in the given runtime, and nil keeps its own.
func TestHostUseRuntimeAdoptsTheGivenRuntime(t *testing.T) {
	host := NewHost(t.TempDir())
	own := host.Runtime()
	host.UseRuntime(nil)
	if host.Runtime() != own {
		t.Fatal("a nil runtime replaced the Host's own")
	}
	shared := extension.CreateExtensionRuntime()
	host.UseRuntime(shared)
	if host.Runtime() != shared {
		t.Fatal("the Host does not use the given runtime")
	}
}
