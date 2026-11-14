package extensionconformance

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestConformance_McpServersChangeFirstHandlerInAnotherProcessIsSentAndItsFailureReported pins what the early acknowledgment of a subprocess
// first handler leaves untouched. Pi's listener calls `void this.emit(...)` (runner.ts:465-468); emit awaits every handler and hands a throw
// to the error listeners as { extensionPath, event, error } (runner.ts:1103-1126, emitError at runner.ts:749-753). The registration that fired
// the event does not wait for a handler in another process: its proxy acknowledges before it writes a frame (subprocess
// managedExt.makeEventHandler). That acknowledgment replaces neither the request nor its outcome.
//
// The SDK extension is the only mcp_servers_change handler, so the first. Its handler reads the active tools from the host, which is its
// first host call, then reports the servers it was given and fails. The host answers that read only after the registering call returned:
// a registration that waited for the handler, or for its first host call, never returns; a proxy that acknowledged without sending leaves
// no report from the handler; and a dropped failure leaves the error listener empty.
func TestConformance_McpServersChangeFirstHandlerInAnotherProcessIsSentAndItsFailureReported(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	rows := []string{"subprocess-go", "subprocess-python", "subprocess-go-packed", "subprocess-python-packed"}
	ran := 0
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if !slices.Contains(rows, tc.name) {
			continue
		}
		ran++
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.host == nil || h.bridge == nil || h.ui == nil {
				t.Fatalf("%s has no subprocess host bridge", tc.name)
			}

			returned := make(chan struct{})
			release := sync.OnceFunc(func() { close(returned) })
			t.Cleanup(release)
			h.bridge.SetHostAction("getActiveTools", func() []string {
				<-returned
				return []string{"read"}
			})

			runner := inproc.NewRunner(h.host.Extensions(), t.TempDir(), h.host.Runtime())
			t.Cleanup(runner.Shutdown)
			runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
			var mu sync.Mutex
			var failures []extension.ExtensionError
			runner.AddErrorListener(func(err *extension.ExtensionError) {
				mu.Lock()
				defer mu.Unlock()
				failures = append(failures, *err)
			})
			reported := func() []extension.ExtensionError {
				mu.Lock()
				defer mu.Unlock()
				return slices.Clone(failures)
			}

			runConformanceCommandArgs(t, h, "event_probe_fail", "mcp_servers_change")
			pollUntilConformance(t, "the host never received the SDK's mcp_servers_change subscription", func() bool {
				return runner.HasHandlers("mcp_servers_change")
			})

			registered := make(chan error, 1)
			go func() {
				registered <- h.host.Runtime().RegisterMcpServer("/ext/conformance-ack.go", "conformance-ack", json.RawMessage(`{"url":"https://conformance-ack.invalid/mcp"}`))
			}()
			select {
			case err := <-registered:
				if err != nil {
					t.Fatal(err)
				}
			case <-testbudget.Context(t).Done():
				t.Fatal("the registration waited for the handler in the extension process")
			}
			if got := reported(); len(got) != 0 {
				t.Fatalf("the handler ended before the registration returned: %+v", got)
			}
			release()

			const sent = "event_probe_fail:mcp_servers_change:conformance-ack:info"
			pollUntilConformance(t, "the event never reached the SDK's handler", func() bool {
				return slices.Contains(h.ui.Recorded(), sent)
			})
			pollUntilConformance(t, "the handler's failure never reached the error listener", func() bool {
				return len(reported()) > 0
			})
			got := reported()[0]
			if got.Event != "mcp_servers_change" || !strings.Contains(got.Error, "event_probe_fail: mcp_servers_change handler failed") {
				t.Fatalf("reported failure = %+v, want the handler's error under mcp_servers_change", got)
			}
			if !slices.ContainsFunc(h.host.Extensions(), func(ext extension.Extension) bool {
				return got.ExtensionPath == ext.Path || got.ExtensionPath == ext.ResolvedPath
			}) {
				t.Fatalf("reported failure names %q, which is no extension of the host", got.ExtensionPath)
			}
		})
	}
	if ran != len(rows) {
		t.Fatalf("ran %d of the rows %v", ran, rows)
	}
}
