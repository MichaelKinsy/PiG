//go:build !pig_strip_mcp

// SPDX-License-Identifier: MIT

package coding

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi registers a server's tools, the tools it no longer offers as hidden, and the resource tools in one synchronous turn
// (extensions/mcp/index.ts:387-463), so nothing that runs after it, a prompt included, sees part of them. Pig registers them as one
// change: every state of the Session's tools has all of the server's tools and the resource tools, or none of them.
func TestAgentSessionMCPRegistersAServersToolsAsOneChange(t *testing.T) {
	const offered = 60
	tools := make([]map[string]any, offered)
	for i := range tools {
		tools[i] = map[string]any{"name": "t" + strconv.Itoa(i), "description": "tool", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}
	}
	// The delayed initialize lets the observer start before the server's tools arrive.
	h := setupMCP(t, extension.McpExposureDirect, func() []map[string]any { return tools }, mcpSessionOptions{resources: true, skipToolWait: true, initializeDelay: 50 * time.Millisecond})
	resourceTools := []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"}
	deadline := time.Now().Add(10 * time.Second)
	for {
		server, resources := 0, 0
		for _, tool := range h.session.GetAllTools() {
			switch {
			case strings.HasPrefix(tool.Name, "mcp__docs__"):
				server++
			case tool.Name == resourceTools[0] || tool.Name == resourceTools[1] || tool.Name == resourceTools[2]:
				resources++
			}
		}
		if server == offered && resources == len(resourceTools) {
			return
		}
		if server != 0 || resources != 0 {
			t.Fatalf("the Session's tools had %d of the server's %d tools and %d of the %d resource tools", server, offered, resources, len(resourceTools))
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the server's tools to be registered")
		}
	}
}
