//go:build !pig_strip_mcp

package coding

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/coding-agent/test/suite/agent-session-mcp.test.ts (Pi 1.0.0), "AgentSession MCP tools after resume and
// reload": a deferred `docs` server whose tools tool_search loads, then a second Session over the same in-memory session
// (resume) or the same Session after `/reload`. The built-in tool-search and MCP extensions load the way the CLI loads them.

// mcpConnectedNames counts the connections of the fake server by server name (`connected` of the test).
type mcpConnectedNames struct {
	mu    sync.Mutex
	names []string
}

func (c *mcpConnectedNames) add(name string) {
	c.mu.Lock()
	c.names = append(c.names, name)
	c.mu.Unlock()
}

func (c *mcpConnectedNames) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.names)
}

type mcpResumeSession struct {
	*mcpSession
	connected *mcpConnectedNames
	// loadExtensions loads the extensions again, as the test's resource loader reload does.
	loadExtensions func() []extension.Extension
}

// setupMCPResume is setup(sessionManager, extensionFactories, initializeDelayMs) of the test: a deferred `docs` server that
// answers `initialize` after initializeDelay. sessionManager, when set, is the session the new Session resumes.
func setupMCPResume(t *testing.T, sessionManager *icodingagent.Session, extensionFactories []func() extension.Extension, initializeDelay time.Duration) *mcpResumeSession {
	t.Helper()
	s := &mcpSession{calls: &mcpCallLog{}, notes: &mcpNotifications{}}
	connected := &mcpConnectedNames{}
	servers := []mcpext.McpServerEntry{{
		Name: "docs", Config: extension.McpServerConfig{URL: "http://unused.invalid", Exposure: extension.McpExposureDeferred}, Source: "test",
	}}
	options := builtin.Options{Mcp: mcpext.Options{
		LoadConfig: func(mcpext.EventContext) mcpext.LoadedMcpConfig { return mcpext.LoadedMcpConfig{Servers: servers} },
		CreateTransport: func(entry mcpext.McpServerEntry, _ string, _ mcp.AuthProvider) (mcp.Transport, error) {
			connected.add(entry.Name)
			client, server := mcpFakeServer(s.calls, func() []map[string]any { return mcpServerTools }, false, mcpFakeServerOptions{initializeDelay: initializeDelay})
			s.mu.Lock()
			s.servers = append(s.servers, server)
			s.mu.Unlock()
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	}}
	loadExtensions := func() []extension.Extension {
		var extensions []extension.Extension
		for _, factory := range extensionFactories {
			extensions = append(extensions, factory())
		}
		return append(extensions, loadMcpBuiltin(t, "tool-search", options), loadMcpBuiltin(t, "mcp", options))
	}
	s.recoveryHarness = newBoundaryHarness(t, harnessOptions{extensions: loadExtensions(), sessionManager: sessionManager})
	s.ui = &mcpTestUI{UIContext: extension.NoopUIContext, notes: s.notes}
	// `/reload` emits session_start only to bound extensions.
	if err := s.session.BindExtensions(t.Context(), ExtensionBindings{UIContext: s.ui}); err != nil {
		t.Fatal(err)
	}
	return &mcpResumeSession{mcpSession: s, connected: connected, loadExtensions: loadExtensions}
}

// reload is `harness.session.reload()` (agent-session.ts:3612-3650) for a Session whose extensions are bound: the old
// runner gets session_shutdown with reason reload, the settings are read again, the extensions load again into a new
// runner that replaces the old one, and, since the Session has bindings, the new runner is bound and gets session_start
// with reason reload. The CLI runs the same steps (cmd/pig headless_reload.go reloadHeadless and the mode's rebind).
func (s *mcpResumeSession) reload(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	if previous := s.session.ExtensionRunner(); previous != nil && previous.HasHandlers(icodingagent.EventSessionShutdown) {
		if _, err := previous.Emit(ctx, extension.SessionShutdownEvent{Type: icodingagent.EventSessionShutdown, Reason: "reload"}); err != nil {
			t.Fatal(err)
		}
	}
	s.session.ReloadSettings()
	defer s.session.DiscardAddedDefaultTools()
	runner, err := s.session.ReloadExtensions(s.loadExtensions())
	if err != nil {
		t.Fatal(err)
	}
	s.session.bindSessionExtensions(runner, ExtensionBindings{UIContext: s.ui})
	if _, err := runner.Emit(ctx, extension.SessionStartEvent{Type: icodingagent.EventSessionStart, Reason: "reload"}); err != nil {
		t.Fatal(err)
	}
	runner.ReportUnhandledMcpServers()
}

// loadDocsSearch is loadDocsSearch of the test: tool_search loads mcp__docs__search.
func (s *mcpResumeSession) loadDocsSearch(t *testing.T) {
	t.Helper()
	s.respond(
		mcpToolCalls(mcpToolCall{"tool_search", ai.JsonObject{"query": "search the docs", "limit": float64(1)}}),
		boundaryReply("loaded", ai.StopReasonStop, 0),
	)
	s.prompt(t, "load")
	if !slices.Contains(s.session.ActiveToolNames(), "mcp__docs__search") {
		t.Fatalf("active tools = %v, want mcp__docs__search", s.session.ActiveToolNames())
	}
}

func TestAgentSessionMCPToolsAfterResumeAndReloadDeclaresToolsToolSearchLoadedAgainOnResumeOnceTheirServerConnects(t *testing.T) {
	first := setupMCPResume(t, nil, nil, 0)
	first.loadDocsSearch(t)

	// The session restores its tools before the server connects again.
	second := setupMCPResume(t, first.session.Inner(), nil, 0)
	mcpWaitFor(t, "mcp__docs__search to be active", func() bool {
		return slices.Contains(second.session.ActiveToolNames(), "mcp__docs__search")
	})
	second.respond(mcpToolCalls(mcpToolCall{"mcp__docs__search", ai.JsonObject{"query": "again"}}), mcpDone())
	second.prompt(t, "use it")

	requireEqual(t, "mcp__docs__search result", mcpText(second.toolResult(t, "mcp__docs__search")), "again guide\nagain faq")
	var removals []*ai.SystemMessage
	for _, message := range second.systemMessages() {
		if len(message.ToolsRemoved) > 0 {
			removals = append(removals, message)
		}
	}
	if len(removals) != 0 {
		t.Fatalf("system messages that remove tools = %#v, want none", removals)
	}
}

func TestAgentSessionMCPToolsAfterResumeAndReloadRestoredToolsWhenAnExtensionSetsTheLoadoutBeforeTheyRegister(t *testing.T) {
	for _, tc := range []struct {
		name    string
		loadout []string
		kept    bool
	}{
		{name: "drops", loadout: []string{"read"}, kept: false},
		{name: "keeps", loadout: nil, kept: true},
	} {
		t.Run(tc.name+" restored tools when an extension sets the loadout before they register", func(t *testing.T) {
			first := setupMCPResume(t, nil, nil, 0)
			first.loadDocsSearch(t)

			// Like plan mode restoring its tools, or an extension adding one to the current loadout.
			setLoadout := func() extension.Extension {
				return extension.Extension{
					Name: "set-loadout", Path: "/set-loadout", ResolvedPath: "/set-loadout",
					Handlers: map[string][]extension.HandlerFn{
						"session_start": {func(args ...any) (any, error) {
							ctx, _ := args[1].(context.Context)
							pi := extension.FromContext(ctx)
							if tc.loadout != nil {
								pi.SetActiveTools(slices.Clone(tc.loadout))
							} else {
								pi.SetActiveTools(append(pi.GetActiveTools(), "read"))
							}
							return nil, nil
						}},
					},
				}
			}
			second := setupMCPResume(t, first.session.Inner(), []func() extension.Extension{setLoadout}, 0)
			mcpWaitFor(t, "mcp__docs__search to be registered", func() bool { return second.hasTool("mcp__docs__search") })

			if got := slices.Contains(second.session.ActiveToolNames(), "mcp__docs__search"); got != tc.kept {
				t.Fatalf("mcp__docs__search active = %v, want %v (active tools %v)", got, tc.kept, second.session.ActiveToolNames())
			}
		})
	}
}

func TestAgentSessionMCPToolsAfterResumeAndReloadDoesNotActivateRestoredToolsThatRegisterAfterTheNextPromptStarts(t *testing.T) {
	first := setupMCPResume(t, nil, nil, 0)
	first.loadDocsSearch(t)

	// The first prompt does not wait for servers without direct tools.
	second := setupMCPResume(t, first.session.Inner(), nil, 200*time.Millisecond)
	second.respond(mcpDone())
	second.prompt(t, "go")
	mcpWaitFor(t, "mcp__docs__search to be registered", func() bool { return second.hasTool("mcp__docs__search") })

	if slices.Contains(second.session.ActiveToolNames(), "mcp__docs__search") {
		t.Fatalf("active tools = %v, want mcp__docs__search inactive", second.session.ActiveToolNames())
	}
}

func TestAgentSessionMCPToolsAfterResumeAndReloadDeclaresToolsToolSearchLoadedAgainAfterReload(t *testing.T) {
	h := setupMCPResume(t, nil, nil, 0)
	h.loadDocsSearch(t)

	h.reload(t)

	mcpWaitFor(t, `connections ["docs","docs"]`, func() bool { return slices.Equal(h.connected.all(), []string{"docs", "docs"}) })
	mcpWaitFor(t, "mcp__docs__search to be active", func() bool {
		return slices.Contains(h.session.ActiveToolNames(), "mcp__docs__search")
	})
}
