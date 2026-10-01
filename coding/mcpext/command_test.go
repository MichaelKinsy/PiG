package mcpext_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// The `/mcp` command (.upstream/v0.99.1/packages/coding-agent/src/extensions/mcp/index.ts:901-980). Upstream has no
// test file for it; the expectations are the messages and branches of its handler and getArgumentCompletions.

type leveledNote struct{ message, level string }

type leveledNotes struct {
	mu    sync.Mutex
	notes []leveledNote
}

func (n *leveledNotes) notify(message, level string) {
	n.mu.Lock()
	n.notes = append(n.notes, leveledNote{message, level})
	n.mu.Unlock()
}

func (n *leveledNotes) all() []leveledNote {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.notes)
}

type commandHarness struct {
	ext     *mcpext.Extension
	notes   *leveledNotes
	ctx     mcpext.CommandContext
	asked   [][]string
	answer  string
	managed int
	updates []mcpext.McpServerConfigPatch
	// saveError, when set, is what saving a config change fails with.
	saveError error
	// err is what the last run of the command returned.
	err error
}

// newCommandHarness starts a session with one fake server per name; a name starting with "stdio-" is a stdio server.
func newCommandHarness(t *testing.T, names ...string) *commandHarness {
	t.Helper()
	return newCommandHarnessWithCredentials(t, &mcpext.InMemoryAuthStorageBackend{}, names...)
}

// newCommandHarnessWithCredentials is newCommandHarness with the credential store kept in backend.
func newCommandHarnessWithCredentials(t *testing.T, backend mcpext.AuthStorageBackend, names ...string) *commandHarness {
	t.Helper()
	h := &commandHarness{notes: &leveledNotes{}}
	host := newFakeHost()
	host.registerBuiltin(mcpext.CodemodeToolName, "builtin:codemode")
	host.registerBuiltin(mcpext.ToolSearchToolName, "builtin:tool-search")
	var entries []mcpext.McpServerEntry
	for _, name := range names {
		config := extension.McpServerConfig{URL: "http://unused.invalid"}
		if strings.HasPrefix(name, "stdio-") {
			config = extension.McpServerConfig{Command: "unused"}
		}
		entries = append(entries, mcpext.McpServerEntry{Name: name, Config: config, Source: "test"})
	}
	calls := &callLog{}
	h.ext = mcpext.New(host, mcpext.Options{
		LoadConfig: func(mcpext.EventContext) mcpext.LoadedMcpConfig { return mcpext.LoadedMcpConfig{Servers: entries} },
		CreateTransport: func(entry mcpext.McpServerEntry, _ string, _ mcp.AuthProvider) (mcp.Transport, error) {
			if strings.HasPrefix(entry.Name, "fail-") {
				return nil, errors.New("connection refused\nsecond line")
			}
			client, server := createFakeServer(calls, func() []map[string]any { return serverTools }, false)
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(backend, ""),
		LogPath:     t.TempDir() + "/mcp.log",
		UpdateConfig: func(_ mcpext.McpServerEntry, patch mcpext.McpServerConfigPatch) error {
			h.updates = append(h.updates, patch)
			return h.saveError
		},
		AgentDir:      "/agent",
		ConfigDirName: ".pi",
	})
	events := mcpext.EventContext{Cwd: t.TempDir(), IsProjectTrusted: func() bool { return true }, Notify: h.notes.notify}
	h.ctx = mcpext.CommandContext{
		EventContext: events,
		Mode:         extension.ModePrint,
		HasUI:        false,
		Select: func(_ context.Context, _ string, options []string) (string, bool) {
			h.asked = append(h.asked, slices.Clone(options))
			return h.answer, h.answer != ""
		},
		ShowManager: func(context.Context, func(mcpext.McpUi) error) error { h.managed++; return nil },
	}
	h.ext.SessionStart(events)
	// The first prompt no longer waits for servers without direct tools (0.99.2), so the harness waits for startup itself.
	h.ext.Pending()
	h.ext.BeforeAgentStart(events, nil)
	t.Cleanup(h.ext.SessionShutdown)
	return h
}

func (h *commandHarness) run(t *testing.T, args string) []leveledNote {
	t.Helper()
	before := len(h.notes.all())
	h.err = h.ext.RunCommand(t.Context(), args, h.ctx)
	return h.notes.all()[before:]
}

func expectNotes(t *testing.T, got []leveledNote, want ...leveledNote) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("notifications = %q, want %q", got, want)
	}
}

// index.ts:928-930: without arguments the terminal UI opens the manager; other modes print formatStatus().
func TestMcpCommandWithoutArgumentsShowsTheManagerInTheTerminalUIAndTheStatusElsewhere(t *testing.T) {
	h := newCommandHarness(t, "docs")
	expectNotes(t, h.run(t, "  "), leveledNote{h.ext.FormatStatus(), "info"})
	if h.managed != 0 {
		t.Fatal("the manager opened outside the terminal UI")
	}
	h.ctx.Mode = extension.ModeTUI
	h.ctx.HasUI = true
	expectNotes(t, h.run(t, ""))
	if h.managed != 1 {
		t.Fatalf("manager opened %d times", h.managed)
	}
}

// index.ts:160,931-934,974: more than one argument, or an action that is not login, logout or reconnect, is a usage warning.
func TestMcpCommandWarnsWithTheUsageForAnUnknownActionOrExtraArguments(t *testing.T) {
	h := newCommandHarness(t, "docs")
	if mcpext.McpUsage != "Usage: /mcp, /mcp login [server], /mcp logout [server], /mcp reconnect [server]" {
		t.Fatalf("usage = %q", mcpext.McpUsage)
	}
	for _, args := range []string{"bogus", "login docs extra", "reconnect a b", "status"} {
		expectNotes(t, h.run(t, args), leveledNote{mcpext.McpUsage, "warning"})
	}
}

// index.ts:766-771: signing in outside interactive mode is an error, after the server is chosen.
func TestMcpLoginNeedsInteractiveMode(t *testing.T) {
	h := newCommandHarness(t, "docs")
	expectNotes(t, h.run(t, "login docs"), leveledNote{`Signing in to MCP server "docs" requires interactive mode.`, "error"})
}

// index.ts:236-244 pickServer: a name that matches no server, and a server that does not use OAuth.
func TestMcpCommandNamesTheServerItCannotUse(t *testing.T) {
	h := newCommandHarness(t, "docs", "stdio-tool")
	expectNotes(t, h.run(t, "logout missing"), leveledNote{`No MCP server named "missing".`, "error"})
	expectNotes(t, h.run(t, "login stdio-tool"),
		leveledNote{"No enabled MCP server uses OAuth. Only HTTP servers without an Authorization header do.", "error"})
	expectNotes(t, h.run(t, "reconnect missing"), leveledNote{`No MCP server named "missing".`, "error"})
}

// index.ts:733-744: with no name, no eligible server is an info notice.
func TestMcpCommandWithoutAServerToPickSaysSo(t *testing.T) {
	h := newCommandHarness(t, "stdio-tool")
	expectNotes(t, h.run(t, "logout"),
		leveledNote{"No enabled MCP server uses OAuth. Only HTTP servers without an Authorization header do.", "info"})
	h = newCommandHarness(t)
	expectNotes(t, h.run(t, "reconnect"), leveledNote{"No enabled MCP server to reconnect.", "info"})
	if len(h.asked) != 0 {
		t.Fatalf("asked %v", h.asked)
	}
}

// index.ts:747: logout of an OAuth server without stored credentials.
func TestMcpLogoutWithoutStoredCredentials(t *testing.T) {
	h := newCommandHarness(t, "docs")
	expectNotes(t, h.run(t, "logout docs"), leveledNote{`No stored credentials for MCP server "docs".`, "info"})
	// A single eligible server needs no question.
	expectNotes(t, h.run(t, "logout"), leveledNote{`No stored credentials for MCP server "docs".`, "info"})
	if len(h.asked) != 0 {
		t.Fatalf("asked %v", h.asked)
	}
}

// index.ts:951-969: reconnect names the state the server ended in, and activates discovery.
func TestMcpReconnectReportsTheStateOfTheServer(t *testing.T) {
	h := newCommandHarness(t, "docs")
	expectNotes(t, h.run(t, "reconnect docs"), leveledNote{`Reconnected to MCP server "docs" (connected · 3 tools).`, "info"})
	expectNotes(t, h.run(t, "reconnect"), leveledNote{`Reconnected to MCP server "docs" (connected · 3 tools).`, "info"})
}

// index.ts:745-751: several eligible servers and no preferred one are asked with ctx.ui.select, titled "MCP server",
// listing the names in order; a cancelled question does nothing.
func TestMcpCommandAsksWhichServerWhenSeveralQualify(t *testing.T) {
	h := newCommandHarness(t, "alpha", "beta")
	h.answer = "beta"
	expectNotes(t, h.run(t, "reconnect"), leveledNote{`Reconnected to MCP server "beta" (connected · 3 tools).`, "info"})
	if !slices.Equal(h.asked[0], []string{"alpha", "beta"}) {
		t.Fatalf("options = %v", h.asked)
	}
	h.answer = ""
	expectNotes(t, h.run(t, "reconnect"))
	if len(h.asked) != 2 {
		t.Fatalf("asked %d times", len(h.asked))
	}
}

// index.ts:904-924: completions of the action, then of the server; null where nothing applies.
func TestMcpCommandCompletions(t *testing.T) {
	h := newCommandHarness(t, "docs", "stdio-tool")
	values := func(items []extension.AutocompleteItem) []string {
		var out []string
		for _, item := range items {
			out = append(out, item.Value+"|"+item.Label+"|"+item.Description)
		}
		return out
	}
	for _, tc := range []struct {
		prefix string
		want   []string
	}{
		{"", []string{"login |login|", "logout |logout|", "reconnect |reconnect|"}},
		{"  lo", []string{"login |login|", "logout |logout|"}},
		{"re", []string{"reconnect |reconnect|"}},
		{"x", nil},
		// The server is being typed: only servers that fit the action.
		{"login ", []string{"login docs|docs|connected · 3 tools"}},
		{"reconnect ", []string{"reconnect docs|docs|connected · 3 tools", "reconnect stdio-tool|stdio-tool|connected · 3 tools"}},
		{"reconnect st", []string{"reconnect stdio-tool|stdio-tool|connected · 3 tools"}},
		{"logout z", nil},
		{"status ", nil},
		{"login docs extra", nil},
	} {
		if got := values(h.ext.CompleteCommand(tc.prefix)); !slices.Equal(got, tc.want) {
			t.Errorf("CompleteCommand(%q) = %q, want %q", tc.prefix, got, tc.want)
		}
	}
}

// index.ts:901: the extension registers `mcp` with its description and handler on the extension API.
func TestMcpFactoryRegistersTheCommand(t *testing.T) {
	api := &recordingAPI{}
	mcpext.Factory(mcpext.Options{LogPath: t.TempDir() + "/mcp.log", AgentDir: t.TempDir(), ConfigDirName: ".pi"})(api)
	command, ok := api.commands["mcp"]
	if !ok {
		t.Fatal("no /mcp command registered")
	}
	if command.Description != "Manage MCP servers: sign in, reconnect, enable or disable, and change exposure" {
		t.Fatalf("description = %q", command.Description)
	}
	if command.Handler == nil || command.GetArgumentCompletions == nil {
		t.Fatal("handler or completions missing")
	}
	items, err := command.GetArgumentCompletions("lo")
	if err != nil || len(items) != 2 || items[0].Value != "login " {
		t.Fatalf("completions = %v, %v", items, err)
	}
}

// recordingAPI is the extension API with the calls the factory makes recorded or ignored.
type recordingAPI struct {
	extension.API
	commands map[string]extension.CommandOptions
}

func (a *recordingAPI) RegisterCommand(name string, options extension.CommandOptions) {
	if a.commands == nil {
		a.commands = map[string]extension.CommandOptions{}
	}
	a.commands[name] = options
}
func (*recordingAPI) OnSessionStart(func(context.Context, extension.SessionStartEvent) error) {}
func (*recordingAPI) OnBeforeAgentStart(func(context.Context, extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error)) {
}
func (*recordingAPI) OnToolCall(func(context.Context, extension.ToolCallEvent) (extension.ToolCallEventResult, error)) {
}
func (*recordingAPI) OnTurnStart(func(context.Context, extension.TurnStartEvent) error) {}
func (*recordingAPI) OnMcpServersChange(func(context.Context, extension.McpServersChangeEvent) error) {
}
func (*recordingAPI) OnSessionShutdown(func(context.Context, extension.SessionShutdownEvent) error) {}

// index.ts:745-751: of several servers, the only one that failed or disconnected is the one a nameless reconnect means.
func TestMcpReconnectPrefersTheOnlyServerThatFailed(t *testing.T) {
	h := newCommandHarness(t, "alpha", "fail-beta")
	got := h.run(t, "reconnect")
	if len(h.asked) != 0 {
		t.Fatalf("asked %v", h.asked)
	}
	if len(got) != 1 || got[0].level != "error" || !strings.Contains(got[0].message, "connection refused") {
		t.Fatalf("notifications = %q", got)
	}
}

// failingCredentials is a credential store whose file cannot be read or written.
type failingCredentials struct{}

var errCredentials = errors.New("mcp-auth.json is unreadable")

func (failingCredentials) WithLock(func(string, bool) (*string, error)) error { return errCredentials }

// index.ts:465-471,943-950: signOut awaits the credential removal, so a removal that throws rejects the handler (the
// runner reports it as a command error), prints no sign-out notice, and leaves the connection signed in.
func TestMcpLogoutFailureIsTheCommandsError(t *testing.T) {
	h := newCommandHarnessWithCredentials(t, failingCredentials{}, "docs")
	expectNotes(t, h.run(t, "logout docs"))
	if !errors.Is(h.err, errCredentials) {
		t.Fatalf("command error = %v, want %v", h.err, errCredentials)
	}
	if items := h.ext.CompleteCommand("logout "); len(items) != 1 || items[0].Description != "connected · 3 tools" {
		t.Fatalf("server after the failed logout = %+v, want it still connected", items)
	}
}

// index.ts:928-929: a manager view that cannot be shown rejects the handler instead of becoming a notice.
func TestMcpManagerThatCannotBeShownIsTheCommandsError(t *testing.T) {
	h := newCommandHarness(t, "docs")
	h.ctx.Mode = extension.ModeTUI
	failed := errors.New("no terminal")
	h.ctx.ShowManager = func(context.Context, func(mcpext.McpUi) error) error { return failed }
	expectNotes(t, h.run(t, ""))
	if !errors.Is(h.err, failed) {
		t.Fatalf("command error = %v, want %v", h.err, failed)
	}
}

// index.ts:906: the prefix is split with /\s+/ after trimStart(), JavaScript's whitespace, so a server name ending in a
// character whose last UTF-8 byte is 0xA0 or 0x85 is still one word, and U+0085 is not a separator.
func TestMcpCommandCompletionsSplitOnJavaScriptWhitespace(t *testing.T) {
	h := newCommandHarness(t, "voilà", "a\u0085b")
	for prefix, want := range map[string]string{"login voilà": "login voilà", "logout a\u0085b": "logout a\u0085b", "\uFEFFlogin vo": "login voilà"} {
		items := h.ext.CompleteCommand(prefix)
		if len(items) != 1 || items[0].Value != want {
			t.Errorf("CompleteCommand(%q) = %+v, want one item %q", prefix, items, want)
		}
	}
}
