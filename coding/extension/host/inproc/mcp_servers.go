package inproc

// Ports packages/coding-agent/src/core/extensions/runner.ts (mcpServers change listener, reportUnhandledMcpServers).

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/internal/handleryield"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// eventMcpServersChange is the event type fired when an extension registers or unregisters an MCP server after the runner bound.
const eventMcpServersChange = "mcp_servers_change"

// mcpServersState owns the mcp_servers_change emissions and remembers the servers already reported as unhandled.
type mcpServersState struct {
	mu       sync.Mutex
	reported map[string]struct{}
	// emissions counts the emissions that run. drained is set once the runner waits for them; none starts after it.
	emissions sync.WaitGroup
	drained   bool
}

// onMcpServersChange is the registry's change listener: upstream's `void this.emit(...)` followed by reportUnhandledMcpServers. emit has no await before its handler loop (runner.ts:1103-1110), so upstream runs the first handler up to its first await on the stack of the registration, and every event is a chain of its own. Here every event is one emission on a goroutine the runner owns, and the listener goes on once the first handler acknowledged: it reported that it stops running synchronously (handleryield.Report), or it returned. The handlers after the first run on the emission's goroutine beside the registering code; upstream starts them after that code's synchronous run.
//
// The listener does not wait in two cases, in which the registration returns before the first handler's synchronous part as it did before: a caller on the input and render loop (CallerOnOwnerLoop), because the handler runs on another goroutine and may need that loop; and a first handler in another process, whose proxy acknowledges before it writes a frame (subprocess managedExt.makeEventHandler), so a registration never waits on extension IPC.
//
// upstream: runner.ts:465-468
func (r *Runner) onMcpServersChange() {
	event := extension.McpServersChangeEvent{Type: eventMcpServersChange, Servers: r.runtime.McpServers().List()}
	if entered := r.startMcpServersChange(event); entered != nil && !r.CallerOnOwnerLoop() {
		select {
		case <-entered:
		case <-r.background.Done():
		}
	}
	r.ReportUnhandledMcpServers()
}

// startMcpServersChange starts the emission of event and returns the channel that closes at its first handler's acknowledgment. An invalidated or drained runner starts none and returns nil (Go cancellation addition: upstream's emit would reach a stale extension).
func (r *Runner) startMcpServersChange(event extension.McpServersChangeEvent) <-chan struct{} {
	entered := make(chan struct{})
	enter := sync.OnceFunc(func() { close(entered) })
	ctx := handleryield.With(invocation.WithAcknowledgment(r.background, enter), enter)
	r.mcp.mu.Lock()
	defer r.mcp.mu.Unlock()
	if r.mcp.drained || r.background.Err() != nil {
		return nil
	}
	r.mcp.emissions.Add(1)
	//portlint:allow asyncorder one emission per event, as each upstream `void this.emit` is a chain of its own (runner.ts:465-468); the listener that started it waits for its first handler's acknowledgment before it reports and returns, which orders the first handler's synchronous part before the registering code as upstream's call stack does; the runner owns the goroutine (mcpServersState.emissions, DrainMcpServersChanges)
	go r.emitMcpServersChange(ctx, event, enter)
	return entered
}

// emitMcpServersChange is one emission. An event that no handler takes is acknowledged when the emission ends.
func (r *Runner) emitMcpServersChange(ctx context.Context, event extension.McpServersChangeEvent, enter func()) {
	defer r.mcp.emissions.Done()
	defer enter()
	// Handler failures reach the error listeners through Emit.
	_, _ = r.Emit(ctx, event)
}

// DrainMcpServersChanges waits for the mcp_servers_change emissions the runner started; none starts afterwards. The caller invalidates the runner first, which ends the emissions' context.
func (r *Runner) DrainMcpServersChanges() {
	r.mcp.mu.Lock()
	r.mcp.drained = true
	r.mcp.mu.Unlock()
	r.mcp.emissions.Wait()
}

// ReportUnhandledMcpServers reports each registered MCP server once as a `register_mcp_server` extension error when no loaded extension handles `mcp_servers_change`: nothing connects the servers, for example because another MCP extension replaced the built-in one.
//
// upstream: runner.ts:747-762 (reportUnhandledMcpServers)
func (r *Runner) ReportUnhandledMcpServers() {
	if r.HasHandlers(eventMcpServersChange) {
		return
	}
	for _, server := range r.runtime.McpServers().List() {
		r.mcp.mu.Lock()
		if r.mcp.reported == nil {
			r.mcp.reported = make(map[string]struct{})
		}
		_, done := r.mcp.reported[server.Name]
		r.mcp.reported[server.Name] = struct{}{}
		r.mcp.mu.Unlock()
		if done {
			continue
		}
		r.emitError(&extension.ExtensionError{
			ExtensionPath: server.ExtensionPath,
			Event:         "register_mcp_server",
			Error:         `MCP server "` + server.Name + `" is registered, but no loaded extension connects MCP servers; ` + unhandledMcpServersReason(),
		})
	}
}

// unhandledMcpServersReason says why no extension connects the registered MCP servers. Pi's reason is that another extension
// replaced the built-in MCP support (runner.ts:760).
// pig additive (D92): with mcp stripped nothing replaced it, so the reason names the strip.
func unhandledMcpServersReason() string {
	if pigstrip.Has(pigstrip.ListExtensions, "mcp") {
		return pigstrip.Error("built-in MCP support", pigstrip.ListExtensions, "mcp").Error()
	}
	return "another extension may have replaced the built-in MCP support"
}
