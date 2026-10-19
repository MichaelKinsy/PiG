//go:build !pig_strip_node_extensions

package subprocess

import (
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// A Node extension whose factory fails reports Pi's load error over its socket
// and exits (loader.ts loadExtension: "Extension does not export a valid
// factory function: <path>", "Failed to load extension: <message>"). The host
// must report that error whenever it looks, including after the exit: a
// machine loaded enough to delay the host's read past the process's exit
// failed 1 in 20 runs of TestUpstreamExtensionsDiscovery with process_exited.
// The gates force the two orders that machine produces: the host's Accept
// completes only after the exit, and starts only after it. Each is deterministic
// in the placements the host chooses between: a packed cell and an isolated
// process must report the same error.
func TestNodeFactoryFailureOutranksTheProcessExit(t *testing.T) {
	t.Parallel()
	nodeCellRequireNode(t)
	gates := map[string]acceptGateFunc{
		"accept completes after the exit": func(accept func() (net.Conn, error), exited <-chan struct{}) (net.Conn, error) {
			conn, err := accept()
			<-exited
			return conn, err
		},
		"accept starts after the exit": func(accept func() (net.Conn, error), exited <-chan struct{}) (net.Conn, error) {
			<-exited
			return accept()
		},
	}
	failures := map[string]struct{ source, message string }{
		"no default export": {`export function notDefault(pi) {}`, "Extension does not export a valid factory function: "},
		"factory throws":    {`export default function(pi) { throw new Error("Initialization failed!"); }`, "Failed to load extension: Initialization failed!"},
	}
	for gateName, gate := range gates {
		for _, isolation := range []string{"", "isolated"} {
			for failureName, failure := range failures {
				t.Run(gateName+"/isolation="+isolation+"/"+failureName, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					source := filepath.Join(root, "extensions", "failing.ts")
					write(t, source, failure.source)
					host := NewHost(root)
					host.acceptGate = gate
					t.Cleanup(func() { host.Shutdown("test done") })
					loaded, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "failing", Source: source, Enabled: true, Isolation: isolation}})
					if len(loaded) != 0 || len(errs) != 1 {
						t.Fatalf("loaded %d extensions, errors %v; want the one load error", len(loaded), errs)
					}
					loadErr, ok := errors.AsType[*ExtensionLoadError](errs[0])
					if !ok || !strings.Contains(loadErr.Path, "failing.ts") || !strings.Contains(loadErr.Error(), failure.message) {
						t.Fatalf("error = %v; want path failing.ts and %q", errs[0], failure.message)
					}
					if strings.Contains(loadErr.Error(), "process_exited") {
						t.Fatalf("error = %v; the process exited only after it had said why", errs[0])
					}
				})
			}
		}
	}
}
