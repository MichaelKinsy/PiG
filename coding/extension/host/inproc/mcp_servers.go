package inproc

// Ports packages/coding-agent/src/core/extensions/runner.ts (mcpServers change listener, reportUnhandledMcpServers).

import (
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// eventMcpServersChange is the event type fired when an extension registers or unregisters an MCP server after the runner bound.
const eventMcpServersChange = "mcp_servers_change"

// mcpServersState orders mcp_servers_change delivery and remembers the servers already reported as unhandled.
type mcpServersState struct {
	mu       sync.Mutex
	queue    []extension.McpServersChangeEvent
	draining bool
	reported map[string]struct{}
}

// onMcpServersChange is the registry's change listener. Upstream `void this.emit(...)` starts the handlers without waiting for them, so the servers are captured now and delivered in order on an owned goroutine, and the unhandled report runs at once.
//
// upstream: runner.ts:457-462
func (r *Runner) onMcpServersChange() {
	event := extension.McpServersChangeEvent{Type: eventMcpServersChange, Servers: r.runtime.McpServers()}
	r.mcp.mu.Lock()
	r.mcp.queue = append(r.mcp.queue, event)
	start := !r.mcp.draining
	r.mcp.draining = true
	r.mcp.mu.Unlock()
	if start {
		go r.drainMcpServersChanges()
	}
	r.ReportUnhandledMcpServers()
}

func (r *Runner) drainMcpServersChanges() {
	for {
		r.mcp.mu.Lock()
		if len(r.mcp.queue) == 0 || r.background.Err() != nil {
			r.mcp.queue = nil
			r.mcp.draining = false
			r.mcp.mu.Unlock()
			return
		}
		event := r.mcp.queue[0]
		r.mcp.queue = r.mcp.queue[1:]
		r.mcp.mu.Unlock()
		// Handler failures reach the error listeners through Emit.
		_, _ = r.Emit(r.background, event)
	}
}

// ReportUnhandledMcpServers reports each registered MCP server once as a `register_mcp_server` extension error when no loaded extension handles `mcp_servers_change`: nothing connects the servers, for example because another MCP extension replaced the built-in one.
//
// upstream: runner.ts:747-762 (reportUnhandledMcpServers)
func (r *Runner) ReportUnhandledMcpServers() {
	if r.HasHandlers(eventMcpServersChange) {
		return
	}
	for _, server := range r.runtime.McpServers() {
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
			Error:         `MCP server "` + server.Name + `" is registered, but no loaded extension connects MCP servers; another extension may have replaced the built-in MCP support`,
		})
	}
}
