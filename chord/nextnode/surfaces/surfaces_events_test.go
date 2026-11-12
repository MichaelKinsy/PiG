package surfaces

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Runs testdata/client_events.test.mjs: events (Pi 8d30e6306) over the vendored TypeScript host, which the page's and the
// terminal's clients run. The vendored sources are Pi's own (D601): Go does not reimplement them. A client tree without
// events fails here at its first import (`event` is no export of its index.ts); the React hook itself (`useEvents` in a
// mounted component) is the Chromium test TestReactBindingsCallTheLatestListenerOfUseEventsFollowItsMemberAndStopAtUnmount
// of chord/nextnode/app.
func TestClientEventsReachAListenerCrossALinkAndAreExportedToReact(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on the PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--conditions=source", "--no-warnings", "--import", "./testdata/strip-register.mjs", "--test", "--test-reporter=spec", "testdata/client_events.test.mjs")
	// The vendored React is its production build alone.
	command.Env = append(os.Environ(), "NODE_ENV=production")
	output, err := command.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatal(err)
	}
	// A harness that ran no test also exits 0: the file has three cases.
	if !strings.Contains(string(output), "ℹ pass 3") || !strings.Contains(string(output), "ℹ fail 0") {
		t.Fatalf("node:test did not report 3 passing cases and none failing")
	}
}
