//go:build nocodemode

package builtin_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
)

// docs/specs/builtin-codemode-tool-search.md, "Strip": a build with the `nocodemode` tag has no `builtin:codemode`, keeps
// `builtin:tool-search`, and links neither the sandbox, its wasm module nor wazero.
func TestNocodemodeBuildOmitsCodemodeAndItsEngine(t *testing.T) {
	if _, err := builtin.Resolve("builtin:codemode", builtin.Options{}); err == nil || err.Error() != "Unknown built-in extension: builtin:codemode" {
		t.Errorf("Resolve(builtin:codemode) error = %v", err)
	}
	if entry, err := builtin.Resolve("builtin:tool-search", builtin.Options{}); err != nil || entry.Factory == nil {
		t.Errorf("Resolve(builtin:tool-search) = %+v, %v", entry, err)
	}
	out, err := exec.Command("go", "list", "-tags", "nocodemode", "-deps", "github.com/MichaelKinsy/PiG/cmd/pig").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, linked := range []string{"github.com/tetratelabs/wazero", "github.com/MichaelKinsy/PiG/codemode"} {
		if strings.Contains(string(out), linked+"\n") || strings.Contains(string(out), linked+"/") {
			t.Errorf("a nocodemode build of cmd/pig links %s", linked)
		}
	}
}
