//go:build !pig_strip_mcp && !pig_strip_codemode && !pig_strip_tool_search && !pig_strip_node_extensions

package coding

import (
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

func TestAgentSessionMCPServersRegisteredByExtensionsConnectsServersRegisteredWhileExtensionsLoad(t *testing.T) {
	h, connected := setupMCPRegistered(t, mcpRegisteredOptions{fixtures: []string{"register-plugin"}, env: map[string]string{"MCP_PLUGIN_EXPOSURE": "direct"}})
	h.bind(t)
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))
	h.prompt(t, "start")

	entries := connected.all()
	if len(entries) != 1 || entries[0].Name != "plugin" || entries[0].Scope != "extension" {
		t.Fatalf("connected = %#v", entries)
	}
	if !slices.Contains(h.session.ActiveToolNames(), "mcp__plugin__search") {
		t.Errorf("active tools = %v", h.session.ActiveToolNames())
	}
}

func TestAgentSessionMCPServersRegisteredByExtensionsConnectsAndDisconnectsServersRegisteredDuringTheSession(t *testing.T) {
	h, connected := setupMCPRegistered(t, mcpRegisteredOptions{fixtures: []string{"register-late"}})
	h.bind(t)

	h.respond(mcpToolCalls(mcpToolCall{"register_late", ai.JsonObject{}}), mcpDone())
	h.prompt(t, "register")
	mcpWaitFor(t, "mcp__late__search to be callable", func() bool { return slices.Contains(h.nestedToolNames(), "mcp__late__search") })
	entries := connected.all()
	if len(entries) != 1 || entries[0].Name != "late" {
		t.Fatalf("connected = %#v", entries)
	}
	// Codemode-exposed tools need the codemode tool, which is activated for them.
	if !slices.Contains(h.session.ActiveToolNames(), "codemode") {
		t.Errorf("active tools = %v", h.session.ActiveToolNames())
	}

	h.respond(mcpToolCalls(mcpToolCall{"unregister_late", ai.JsonObject{}}), mcpDone())
	h.prompt(t, "unregister")
	mcpWaitFor(t, "mcp__late__search to be withdrawn", func() bool { return !slices.Contains(h.nestedToolNames(), "mcp__late__search") })
}

// "my_docs" shares the namespace of "my-docs" (#10239).
func TestAgentSessionMCPServersRegisteredByExtensionsPrefersTheMCPJSONServerOverARegisteredServer(t *testing.T) {
	for _, name := range []string{"my-docs", "my_docs"} {
		t.Run(name, func(t *testing.T) {
			configured := mcpext.McpServerEntry{Name: "my-docs", Config: extension.McpServerConfig{URL: "http://config.invalid"}, Source: "mcp.json"}
			h, connected := setupMCPRegistered(t, mcpRegisteredOptions{fixtures: []string{"register-plugin"}, env: map[string]string{"MCP_PLUGIN_NAME": name}, configured: []mcpext.McpServerEntry{configured}})
			h.bind(t)

			mcpWaitFor(t, "the configured server to connect", func() bool { return len(connected.all()) >= 1 })
			time.Sleep(10 * time.Millisecond)
			if got := connected.all(); len(got) != 1 || got[0].Name != configured.Name || got[0].Config.URL != configured.Config.URL || got[0].Source != configured.Source {
				t.Errorf("connected = %#v, want only %#v", got, configured)
			}
		})
	}
}

func TestAgentSessionMCPServersRegisteredByExtensionsReportsRegisteredServersWhenNoExtensionConnectsThem(t *testing.T) {
	h, _ := setupMCPRegistered(t, mcpRegisteredOptions{fixtures: []string{"register-orphan"}, withoutMCP: true})
	var mu sync.Mutex
	var errs []string
	h.session.ExtensionRunner().AddErrorListener(func(err *extension.ExtensionError) {
		mu.Lock()
		errs = append(errs, err.Error)
		mu.Unlock()
	})
	h.bind(t)

	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 1 || !regexp.MustCompile(`MCP server "orphan" is registered, but no loaded extension`).MatchString(errs[0]) {
		t.Errorf("errors = %q", errs)
	}
}
