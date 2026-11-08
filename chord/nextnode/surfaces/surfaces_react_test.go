package surfaces

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Runs testdata/react_stores.test.mjs: Pi's test/node/react/stores.test.ts (3 cases) over the vendored TypeScript host and the
// React bindings' stores, which need no React. The vendored sources are Pi's own (D601): Go does not reimplement them, and this
// is the test Pi ships for them, run by Node (24 or later strips the types; the `source` condition names the sources).
func TestReactWatchStoresReadTheValueCallBackOnChangesGiveUndefinedForNoWatchable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on the PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, node, "--conditions=source", "--no-warnings", "--import", "./testdata/strip-register.mjs", "--test", "testdata/react_stores.test.mjs").CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatal(err)
	}
	// A harness that ran no test also exits 0: Pi's file has three cases.
	if !strings.Contains(string(output), "ℹ pass 3") || !strings.Contains(string(output), "ℹ fail 0") {
		t.Fatalf("node:test did not report 3 passing cases and none failing")
	}
}
