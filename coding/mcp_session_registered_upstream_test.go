//go:build !pig_strip_mcp && !pig_strip_codemode && !pig_strip_tool_search

package coding

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/coding-agent/test/suite/agent-session-mcp.test.ts (Pi 1.0.0), "AgentSession MCP servers registered by
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
		LoadConfig: func(context.Context) mcpext.LoadedMcpConfig {
			return mcpext.LoadedMcpConfig{Servers: opts.configured}
		},
		CreateTransport: func(entry mcpext.McpServerEntry, _ string, _ mcp.AuthProvider) (mcp.Transport, error) {
			connected.add(entry)
			client, server := mcpFakeServer(t, &mcpCallLog{}, func() []map[string]any { return mcpServerTools }, false, mcpFakeServerOptions{})
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	}}
	loaded = append(loaded, loadMcpBuiltin(t, host.Runtime(), "codemode", options))
	if !opts.withoutMCP {
		loaded = append(loaded, loadMcpBuiltin(t, host.Runtime(), "mcp", options))
	}
	s.recoveryHarness = newBoundaryHarness(t, harnessOptions{tools: []agent.AgentTool{}, extensions: loaded, runtime: host.Runtime()})
	return s, connected
}

func (s *mcpSession) bind(t *testing.T) {
	t.Helper()
	// `/reload` emits session_start only to bound extensions.
	s.ui = &mcpTestUI{UIContext: extension.NoopUIContext, notes: s.notes}
	if err := s.session.BindExtensions(t.Context(), ExtensionBindings{UIContext: s.ui}); err != nil {
		t.Fatal(err)
	}
}
