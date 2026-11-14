package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/handleryield"
)

// TestConformance_McpServerRegisteredAtRunTimeIsHandledInsideTheCall pins what an extension reads right after pi.registerMcpServer at
// run time (extensions/loader.ts:456-468). Pi's listener calls `void this.emit(...)` (runner.ts:465-468) and emit runs the first
// mcp_servers_change handler to its first await on the stack of the call (runner.ts:1103-1110), each registration as a chain of its own:
// the built-in MCP extension has listed the server and activated the discovery tool when the call returns
// (extensions/mcp/index.ts:1218-1236), also for a server registered while an earlier one still connects.
//
// Every SDK registers two servers in one synchronous run and reads pi.getActiveTools after each call. The first handler is a Go
// reference on the production Runner that stands for the built-in: in its synchronous part it activates a discovery tool named after
// the servers it was given, then it awaits its connection, which the test never completes. The reads are host state no SDK fallback
// holds: an SDK that does not make the calls, a host that answers before the handler's synchronous part ended, and a host that
// delivers the second event only after the first handler ended all report something else.
func TestConformance_McpServerRegisteredAtRunTimeIsHandledInsideTheCall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
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
				t.Skip("no subprocess host bridge for " + tc.name)
			}

			var mu sync.Mutex
			active := []string{"read"}
			reads := make(chan struct{}, 8)
			h.bridge.SetHostAction("getActiveTools", func() []string {
				select {
				case reads <- struct{}{}:
				default:
				}
				mu.Lock()
				defer mu.Unlock()
				return slices.Clone(active)
			})
			connected := make(chan struct{})
			t.Cleanup(sync.OnceFunc(func() { close(connected) }))
			reference := extension.Extension{Path: "go-reference", ResolvedPath: "go-reference"}
			reference.InitializeEventHandlers()
			reference.AddEventHandler("mcp_servers_change", 1, func(args ...any) (any, error) {
				var names []string
				for _, server := range args[0].(extension.McpServersChangeEvent).Servers {
					if name, ok := strings.CutPrefix(server.Name, "conformance-run-"); ok {
						names = append(names, name)
					}
				}
				// The synchronous part stays open for a moment before it activates the tool. The registering call is inside it, so no
				// read can arrive and the moment passes; a host that answered the call already lets the SDK's read in, which then
				// misses the tool.
				for len(reads) > 0 {
					<-reads // a read made before this event
				}
				select {
				case <-reads:
				case <-time.After(200 * time.Millisecond):
				}
				mu.Lock()
				active = []string{"read", "discovery:" + strings.Join(names, "+")}
				mu.Unlock()
				handleryield.Report(args[1].(context.Context))
				<-connected
				return nil, nil
			})
			runner := inproc.NewRunner(append([]extension.Extension{reference}, h.host.Extensions()...), t.TempDir(), h.host.Runtime())
			t.Cleanup(runner.Shutdown)
			runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)

			reported := func() string {
				for _, n := range h.ui.Recorded() {
					if rest, ok := strings.CutPrefix(n, "mcp_register_run:"); ok {
						return strings.TrimSuffix(rest, ":info")
					}
				}
				return ""
			}
			runConformanceCommand(t, h, "mcp_register_run")
			pollUntilConformance(t, "the SDK never reported what it read after its registrations", func() bool { return reported() != "" })
			if got, want := reported(), "read,discovery:a|read,discovery:a+b"; got != want {
				t.Fatalf("active tools read after each registerMcpServer = %q, want %q", got, want)
			}
		})
	}
}
