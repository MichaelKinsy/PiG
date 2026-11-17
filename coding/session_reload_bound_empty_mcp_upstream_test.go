//go:build !pig_strip_mcp

package coding

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// The motivating case of Pi e03dca7a3843 (#12231): "the MCP extension does not reconnect its servers after a reload" when the host bound the extensions
// with bindExtensions({}) (examples/sdk/13-session-runtime.ts, docs/sdk.md). The built-in MCP extension is loaded the way the CLI loads it; its session_start
// connects the configured server, so a second connection after Session.Reload shows that the reloaded extension was started.
func TestSessionReloadAfterBindExtensionsWithEmptyBindingsReconnectsTheMcpServers(t *testing.T) {
	calls := &mcpCallLog{}
	connected := &mcpConnectedNames{}
	options := builtin.Options{Mcp: mcpext.Options{
		LoadConfig: func(context.Context) mcpext.LoadedMcpConfig {
			return mcpext.LoadedMcpConfig{Servers: []mcpext.McpServerEntry{{Name: "docs", Config: extension.McpServerConfig{URL: "http://unused.invalid", Exposure: extension.McpExposureDeferred}, Source: "test"}}}
		},
		CreateTransport: func(entry mcpext.McpServerEntry, _ string, _ mcp.AuthProvider) (mcp.Transport, error) {
			connected.add(entry.Name)
			client, server := mcpFakeServer(t, calls, func() []map[string]any { return mcpServerTools }, false, mcpFakeServerOptions{})
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	}}
	loadMcp := func() ([]extension.Extension, *extension.ExtensionRuntime) {
		runtime := extension.CreateExtensionRuntime()
		return []extension.Extension{loadMcpBuiltin(t, runtime, "mcp", options)}, runtime
	}
	root := t.TempDir()
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			extensions, runtime := loadMcp()
			return LoadExtensionsResult{Extensions: extensions, Runtime: runtime}, nil
		},
	})
	extensions, runtime := loadMcp()
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: inproc.NewRunner(extensions, root, runtime), ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	mcpWaitFor(t, "the first connection of the docs server", func() bool { return len(connected.all()) == 1 })
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	mcpWaitFor(t, "the docs server to reconnect after the reload", func() bool { return len(connected.all()) == 2 })
}
