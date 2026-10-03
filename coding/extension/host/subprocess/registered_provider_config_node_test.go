package subprocess

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestNodeRegisteredProviderConfigMatchesPi runs one scenario against Pi 0.87.1's ModelRegistry and the Node runtime's same-process ModelRegistry and compares every observation: root identity, author aliases, getter evaluation, reader writes, validation before merge, rejected re-registration and unregister.
func TestNodeRegisteredProviderConfigMatchesPi(t *testing.T) {
	t.Parallel()
	nodeCellRequireNode(t)
	root := findModuleRoot(t)
	dir, err := filepath.Abs("testdata/registered-provider-config")
	if err != nil {
		t.Fatal(err)
	}
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	scenario := filepath.Join(dir, "scenario.mjs")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(testbudget.Context(t), "node", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("node %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	pi := run(filepath.Join(dir, "pi.mjs"), filepath.Join(root, "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"), scenario)
	pig := run(filepath.Join(dir, "pig.mjs"), runtimePath, scenario)
	if pig != pi {
		t.Fatalf("PiG observations differ from Pi\nPi:  %s\nPiG: %s", pi, pig)
	}
}
