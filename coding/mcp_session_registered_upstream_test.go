//go:build !pig_strip_mcp

package coding

import (
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/coding-agent/test/suite/agent-session-mcp.test.ts (Pi 0.99.2), "AgentSession MCP servers registered by
// extensions" (four of its five cases: "rejects names another extension registered" expects the error to be thrown inside
// the factory, where PiG queues a registration made while the factory runs and validates it when the extension registers
// (docs/extension-api-parity.md, registerMcpServer row), so the clash fails the extension's load; the runtime's checks are
// ported in coding/extension/host/inproc/mcp_servers_registration_test.go). The extensions that register servers are Node fixtures (testdata/mcp) loaded by the extension host, whose
// runtime the Session's runner shares, as the CLI wires it; the built-in extensions are the ones the CLI loads.

type mcpConnected struct {
	mu      sync.Mutex
	entries []mcpext.McpServerEntry
}

func (c *mcpConnected) add(entry mcpext.McpServerEntry) {
	c.mu.Lock()
	c.entries = append(c.entries, entry)
	c.mu.Unlock()
}

func (c *mcpConnected) all() []mcpext.McpServerEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.entries)
}

type mcpRegisteredOptions struct {
	fixtures   []string
	env        map[string]string
	configured []mcpext.McpServerEntry
	// withoutMCP leaves the MCP extension out: nothing connects the servers.
	withoutMCP bool
}

// setupMCPRegistered is setup(plugins, configured) of the test. It does not bind the extensions.
func setupMCPRegistered(t *testing.T, opts mcpRegisteredOptions) (*mcpSession, *mcpConnected) {
	t.Helper()
	for name, value := range opts.env {
		t.Setenv(name, value)
	}
	t.Setenv("PIG_HOME", t.TempDir())
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("MCP Session complete") })
	var configs []subprocess.ExtConfig
	for _, name := range opts.fixtures {
		source, err := filepath.Abs(filepath.Join("testdata", "mcp", name+".mjs"))
		if err != nil {
			t.Fatal(err)
		}
		configs = append(configs, subprocess.ExtConfig{Name: name, Source: source, Enabled: true})
	}
	loaded, errs := host.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("LoadAll = %d of %d extensions, errors %v", len(loaded), len(configs), errs)
	}
	s := &mcpSession{calls: &mcpCallLog{}, notes: &mcpNotifications{}}
	connected := &mcpConnected{}
	options := builtin.Options{Mcp: mcpext.Options{
		LoadConfig: func(mcpext.EventContext) mcpext.LoadedMcpConfig {
			return mcpext.LoadedMcpConfig{Servers: opts.configured}
		},
		CreateTransport: func(entry mcpext.McpServerEntry, _ string, _ mcp.AuthProvider) (mcp.Transport, error) {
			connected.add(entry)
			client, server := mcpFakeServer(&mcpCallLog{}, func() []map[string]any { return mcpServerTools }, false, mcpFakeServerOptions{})
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	}}
	loaded = append(loaded, loadMcpBuiltin(t, "codemode", options))
	if !opts.withoutMCP {
		loaded = append(loaded, loadMcpBuiltin(t, "mcp", options))
	}
	s.recoveryHarness = newBoundaryHarness(t, harnessOptions{tools: []agent.AgentTool{}, extensions: loaded, runtime: host.Runtime()})
	return s, connected
}

func (s *mcpSession) bind(t *testing.T) {
	t.Helper()
	if err := s.session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
}

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
