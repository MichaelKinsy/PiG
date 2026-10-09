package extensionconformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// mcpChangeReference is the Go reference for extension.API.OnMcpServersChange: a handler registered on a loaded extension, which the production
// Runner delivers the event to exactly as it delivers it to an SDK extension's handler.
type mcpChangeReference struct {
	extension.API
	ext *extension.Extension

	mu     sync.Mutex
	nextID int
}

func (r *mcpChangeReference) OnMcpServersChange(handler func(ctx context.Context, evt extension.McpServersChangeEvent) error) func() {
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.mu.Unlock()
	r.ext.AddEventHandler("mcp_servers_change", id, func(args ...any) (any, error) {
		return nil, handler(context.Background(), args[0].(extension.McpServersChangeEvent))
	})
	return func() { r.ext.RemoveEventHandler("mcp_servers_change", id) }
}

func serverNames(servers []extension.RegisteredMcpServer) string {
	names := make([]string, len(servers))
	for i, server := range servers {
		names[i] = server.Name
	}
	return strings.Join(names, ",")
}

// TestConformance_McpServersChangeDelivery pins Pi's `pi.on("mcp_servers_change", handler)` (extensions/types.ts:699-709, 1562) and runner.ts:457-462: a
// registration made after the extensions are bound reaches the handler of every SDK, with every registered server, in registration order. The
// registration goes through the host's production registry (ExtensionRuntime.RegisterMcpServer) and the production Runner emits the event; the
// servers each SDK reports are the names that registry holds, and the Go reference handler (extension.API.OnMcpServersChange) sees the same list.
func TestConformance_McpServersChangeDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range sdkHarnessCases() {
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
			if h.host == nil {
				t.Skip("no subprocess host for " + tc.name)
			}

			reference := extension.Extension{Path: "go-reference", ResolvedPath: "go-reference"}
			reference.InitializeEventHandlers()
			var referenceSeen []string
			var referenceMu sync.Mutex
			var api extension.API = &mcpChangeReference{ext: &reference}
			api.OnMcpServersChange(func(_ context.Context, evt extension.McpServersChangeEvent) error {
				referenceMu.Lock()
				referenceSeen = append(referenceSeen, serverNames(evt.Servers))
				referenceMu.Unlock()
				return nil
			})
			runner := inproc.NewRunner(append(h.host.Extensions(), reference), t.TempDir(), h.host.Runtime())
			t.Cleanup(runner.Shutdown)
			runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)

			runConformanceCommandArgs(t, h, "event_probe_on", "mcp_servers_change")
			pollUntilConformance(t, 5*time.Second, "the host never received the SDK's mcp_servers_change subscription", func() bool {
				return runner.HasHandlers("mcp_servers_change")
			})

			reported := func() []string {
				var found []string
				for _, n := range h.ui.Recorded() {
					if rest, ok := strings.CutPrefix(n, "event_probe_servers:"); ok {
						found = append(found, strings.TrimSuffix(rest, ":info"))
					}
				}
				return found
			}
			for i, name := range []string{"conformance-docs", "conformance-search"} {
				config := json.RawMessage(`{"url":"https://` + name + `.invalid/mcp"}`)
				if err := h.host.Runtime().RegisterMcpServer("/ext/"+name+".go", name, config); err != nil {
					t.Fatal(err)
				}
				want := []string{"conformance-docs", "conformance-search"}[:i+1]
				pollUntilConformance(t, 5*time.Second, name+": the registration never reached the SDK's handler", func() bool {
					return len(reported()) > i
				})
				if got := reported()[i]; got != strings.Join(want, ",") {
					t.Fatalf("the SDK reported servers %q after registering %s, want %q", got, name, strings.Join(want, ","))
				}
				pollUntilConformance(t, 5*time.Second, name+": the registration never reached the Go reference handler", func() bool {
					referenceMu.Lock()
					defer referenceMu.Unlock()
					return len(referenceSeen) > i
				})
				referenceMu.Lock()
				if got := referenceSeen[i]; got != strings.Join(want, ",") {
					t.Fatalf("the Go reference saw servers %q, want %q", got, strings.Join(want, ","))
				}
				referenceMu.Unlock()
			}
			if got := reported(); !slices.Equal(got, []string{"conformance-docs", "conformance-docs,conformance-search"}) {
				t.Fatalf("reported = %v", got)
			}
		})
	}
}
